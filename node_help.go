package opts

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"strings"
	"text/template"
)

//data is only used for templating below
type data struct {
	datum        //data is also a datum
	ArgList      *datum
	Flags        []*datum
	Args         []*datum
	Cmds         []*datum
	Order        []string
	Version      string
	Repo, Author string
	ErrMsg       string
}

type datum struct {
	Name, Help, Pad string //Pad is Opt.padWidth many spaces
}

type text struct {
	Text, Def, Env string
}

var DefaultOrder = []string{
	"usage",
	"args",
	"arglist",
	"options",
	"cmds",
	"author",
	"version",
	"repo",
	"errmsg",
}

var DefaultTemplates = map[string]string{
	//the root template simply loops through
	//the 'order' and renders each template by name
	"help": `{{ $root := . }}` +
		`{{range $t := .Order}}{{ templ $t $root }}{{end}}`,
	//sections, from top to bottom
	"usage": `Usage: {{.Name }}` +
		`{{template "usageoptions" .}}` +
		`{{template "usageargs" .}}` +
		`{{template "usagearglist" .}}` +
		`{{template "usagecmd" .}}` + "\n",
	"usageoptions": `{{if .Flags}} [options]{{end}}`,
	"usageargs":    `{{range .Args}} {{.Name}}{{end}}`,
	"usagearglist": `{{if .ArgList}} {{.ArgList.Name}}{{end}}`,
	"usagecmd":     `{{if .Cmds}} <command>{{end}}`,
	//extra help text
	"helpextra": `{{if .Def}}default {{.Def}}{{end}}` +
		`{{if and .Def .Env}}, {{end}}` +
		`{{if .Env}}env {{.Env}}{{end}}`,
	//args and arg section
	"args":    `{{range .Args}}{{template "arg" .}}{{end}}`,
	"arg":     "{{if .Help}}\n{{.Help}}\n{{end}}",
	"arglist": "{{if .ArgList}}{{ if .ArgList.Help}}\n{{.ArgList.Help}}\n{{end}}{{end}}",
	//options
	"options": `{{if .Flags}}` + "\nOptions:\n" +
		`{{ range $opt := .Flags}}{{template "option" $opt}}{{end}}{{end}}`,
	"option": `{{.Name}}{{if .Help}}{{.Pad}}{{.Help}}{{end}}` + "\n",
	//cmds
	"cmds": "{{if .Cmds}}\nCommands:\n" +
		`{{ range $sub := .Cmds}}{{template "cmd" $sub}}{{end}}{{end}}`,
	"cmd": "• {{ .Name }}{{if .Help}} - {{ .Help }}{{end}}\n",
	//extras
	"version": "{{if .Version}}\nVersion:\n{{.Pad}}{{.Version}}\n{{end}}",
	"repo":    "{{if .Repo}}\nRead more:\n{{.Pad}}{{.Repo}}\n{{end}}",
	"author":  "{{if .Author}}\nAuthor:\n{{.Pad}}{{.Author}}\n{{end}}",
	"errmsg":  "{{if .ErrMsg}}\nError:\n{{.Pad}}{{.ErrMsg}}\n{{end}}",
}

var trailingSpaces = regexp.MustCompile(`(?m)\ +$`)

//Help renders the help text as a string
func (o *node) Help() string {
	var err error
	//final attempt at finding the program name
	root := o
	for root.parent != nil {
		root = root.parent
	}
	if root.name == "" {
		if exe, err := os.Executable(); err == nil {
			_, root.name = path.Split(exe)
		} else {
			root.name = "main"
		}
	}
	//add default templates
	for name, str := range DefaultTemplates {
		if _, ok := o.templates[name]; !ok {
			o.templates[name] = str
		}
	}
	//prepare templates
	t := template.New(o.name)
	t = t.Funcs(map[string]interface{}{
		//reimplementation of "template" except with dynamic name
		"templ": func(name string, data interface{}) (string, error) {
			b := &bytes.Buffer{}
			err = t.ExecuteTemplate(b, name, data)
			if err != nil {
				return "", err
			}
			return b.String(), nil
		},
	})
	//verify all templates
	for name, str := range o.templates {
		t, err = t.Parse(fmt.Sprintf(`{{define "%s"}}%s{{end}}`, name, str))
		if err != nil {
			log.Fatalf("Template error: %s: %s", name, err)
		}
	}
	//convert node into template data
	tf := convert(o)
	//execute all templates
	b := &bytes.Buffer{}
	err = t.ExecuteTemplate(b, "help", tf)
	if err != nil {
		log.Fatalf("Template execute: %s", err)
	}
	out := b.String()
	if o.padAll {
		/*
			"foo
			bar"
			becomes
			"
			  foo
			  bar
			"
		*/
		lines := strings.Split(out, "\n")
		for i, l := range lines {
			lines[i] = tf.Pad + l
		}
		out = "\n" + strings.Join(lines, "\n")
	}
	out = trailingSpaces.ReplaceAllString(out, "")
	return out
}

var anyspace = regexp.MustCompile(`[\s]+`)

func convert(o *node) *data {
	names := []string{}
	curr := o
	for curr != nil {
		names = append([]string{curr.name}, names...)
		curr = curr.parent
	}
	name := strings.Join(names, " ")
	//get item help, with optional default values and env names and
	//constrain to a specific line width
	extratmpl, _ := template.New("").Parse(o.templates["helpextra"])
	itemHelp := func(i *item, width int) string {
		b := bytes.Buffer{}
		extratmpl.Execute(&b, &text{Def: i.defstr, Env: i.envName})
		extra := b.String()
		help := i.help
		if help == "" {
			help = extra
		} else if extra != "" {
			help += " (" + extra + ")"
		}
		return constrain(help, width)
	}
	args := make([]*datum, len(o.args))
	for i, arg := range o.args {
		//mark argument as required
		n := "<" + arg.name + ">"
		if arg.defstr != "" { //or optional
			n = "[" + arg.name + "]"
		}
		args[i] = &datum{
			Name: n,
			Help: itemHelp(arg, o.lineWidth),
		}
	}
	var arglist *datum
	if o.arglist != nil {
		n := o.arglist.name + "..."
		if o.arglist.min == 0 { //optional
			n = "[" + n + "]"
		}
		arglist = &datum{
			Name: n,
			Help: itemHelp(&o.arglist.item, o.lineWidth),
		}
	}
	flags := make([]*datum, len(o.flags))
	//calculate padding etc.
	max := 0
	pad := nletters(' ', o.padWidth)
	for i, opt := range o.flags {
		to := &datum{Pad: pad}
		to.Name = "--" + opt.name
		if opt.shortName != "" {
			to.Name += ", -" + opt.shortName
		}
		l := len(to.Name)
		if l > max {
			max = l
		}
		flags[i] = to
	}
	padsInOption := o.padWidth
	optionNameWidth := max + padsInOption
	spaces := nletters(' ', optionNameWidth)
	helpWidth := o.lineWidth - optionNameWidth
	//render each option
	for i, to := range flags {
		//pad all option names to be the same length
		to.Name += spaces[:max-len(to.Name)]
		//constrain help text
		help := itemHelp(o.flags[i], helpWidth)
		//add a margin
		lines := strings.Split(help, "\n")
		for i, l := range lines {
			if i > 0 {
				lines[i] = spaces + l
			}
		}
		to.Help = strings.Join(lines, "\n")
	}
	//commands
	subs := make([]*datum, len(o.cmds))
	i := 0
	for _, s := range o.cmds {
		subs[i] = &datum{
			Name: s.name,
			Help: s.help,
			Pad:  pad,
		}
		i++
	}
	//convert error to string
	err := ""
	if o.erred != nil {
		err = o.erred.Error()
	}
	return &data{
		datum: datum{
			Name: name,
			Help: o.help,
			Pad:  pad,
		},
		Args:    args,
		ArgList: arglist,
		Flags:   flags,
		Cmds:    subs,
		Order:   o.order,
		Version: o.version,
		Repo:    o.repo,
		Author:  o.author,
		ErrMsg:  err,
	}
}
