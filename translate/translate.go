package translate

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Translator
//
// The translation functions internally panic, which gets caught by File
type Translator struct {
	buf         *bytes.Buffer
	indentLevel int
}

func NewTranslator() *Translator {
	return &Translator{
		buf: &bytes.Buffer{},
	}
}

func (t *Translator) WriteTo(w io.Writer) (int64, error) {
	return t.buf.WriteTo(w)
}

func (t *Translator) File(f *syntax.File) (err error) {
	// So I don't have to write if err all the time
	defer func() {
		if v := recover(); v != nil {
			if perr, ok := v.(*UnsupportedError); ok {
				err = perr
				return
			}
			panic(v)
		}
	}()

	for _, stmt := range f.Stmts {
		t.stmt(stmt)
		t.nl()
	}

	for _, comment := range f.Last {
		t.comment(&comment)
	}

	return nil
}

func (t *Translator) stmt(s *syntax.Stmt) {
	for _, comment := range s.Comments {
		t.comment(&comment)
	}

	t.command(s.Cmd)
}

type arithmReturn int

const (
	arithmReturnValue arithmReturn = iota
	arithmReturnStatus
)

func (t *Translator) arithmExpr(e syntax.ArithmExpr, returnValue arithmReturn) {
	switch e := e.(type) {
	case *syntax.BinaryArithm:
		switch e.Op {
		case syntax.Eql:
			switch returnValue {
			case arithmReturnValue:
				t.str("(")
			}
			t.str("test ")
			t.arithmExpr(e.X, arithmReturnValue)
			t.str(" -eq ")
			t.arithmExpr(e.Y, arithmReturnValue)
			switch returnValue {
			case arithmReturnValue:
				t.str("; and echo 1; or echo 0)")
			}
		case syntax.Neq:
			switch returnValue {
			case arithmReturnValue:
				t.str("(")
			}
			t.str("test ")
			t.arithmExpr(e.X, arithmReturnValue)
			t.str(" -ne ")
			t.arithmExpr(e.Y, arithmReturnValue)
			switch returnValue {
			case arithmReturnValue:
				t.str("; and echo 1; or echo 0)")
			}
		default:
			unsupported(e)
		}
	case *syntax.UnaryArithm:
		unsupported(e)
	case *syntax.ParenArithm:
		unsupported(e)
	case *syntax.Word:
		l := e.Lit()
		if l == "" {
			unsupported(e)
		}

		switch returnValue {
		case arithmReturnStatus:
			t.str("test ")
		}
		if syntax.ValidName(l) {
			if expr, ok := literalVariables[l]; ok {
				t.str(expr)
			} else {
				t.printf(`"$%s"`, l)
			}
		} else {
			t.str(l)
		}
		switch returnValue {
		case arithmReturnStatus:
			t.str(" != 0")
		}
	default:
		unsupported(e)
	}
}

func (t *Translator) command(c syntax.Command) {
	switch c := c.(type) {
	case *syntax.ArithmCmd:
		t.arithmExpr(c.X, arithmReturnStatus)
	case *syntax.BinaryCmd:
		t.binaryCmd(c)
	case *syntax.Block:
		// TODO: Maybe need begin/end here, sometimes? Not for function
		t.body(c.Stmts...)
	case *syntax.CallExpr:
		t.callExpr(c)
	case *syntax.CaseClause:
		unsupported(c)
	case *syntax.CoprocClause:
		unsupported(c)
	case *syntax.DeclClause:
		t.declClause(c)
	case *syntax.ForClause:
		if c.Select {
			unsupported(c)
		}
		t.str("for ")
		switch l := c.Loop.(type) {
		case *syntax.WordIter:
			t.printf("%s in", l.Name.Value)
			if l.InPos.IsValid() {
				for _, w := range l.Items {
					t.str(" ")
					t.word(w, false)
				}
			} else {
				unsupported(c)
			}
		default:
			unsupported(c)
		}
		t.indent()
		t.body(c.Do...)
		t.outdent()
		t.str("end")
	case *syntax.FuncDecl:
		t.printf("function %s", c.Name.Value)
		t.indent()
		t.stmt(c.Body)
		t.outdent()
		t.str("end")
	case *syntax.IfClause:
		t.ifClause(c, false)
	case *syntax.LetClause:
		unsupported(c)
	case *syntax.Subshell:
		t.str("fish -c ")
		t.capture(func() {
			t.stmts(c.Stmts...)
		})
	case *syntax.TestClause:
		t.testClause(c)
	case *syntax.TimeClause:
		t.str("time ")
		t.stmt(c.Stmt)
	case *syntax.WhileClause:
		t.str("while ")
		if c.Until {
			t.str("not ")
		}
		t.stmts(c.Cond...)
		t.indent()
		t.body(c.Do...)
		t.outdent()
		t.str("end")
	default:
		unsupported(c)
	}
}

func (t *Translator) testClause(c *syntax.TestClause) {
	t.str("test ")
	t.testExpr(c.X)
}

func (t *Translator) testExpr(e syntax.TestExpr) {
	switch e := e.(type) {
	case *syntax.BinaryTest:
		t.testExpr(e.X)
		switch e.Op {
		case syntax.AndTest:
			t.str(" -a ")
		case syntax.OrTest:
			t.str(" -o ")
		case syntax.TsMatch:
			t.str(" = ")
		case syntax.TsNoMatch:
			t.str(" != ")
		case syntax.TsEql,
			syntax.TsNeq,
			syntax.TsLeq,
			syntax.TsGeq,
			syntax.TsLss,
			syntax.TsGtr:
			t.printf(" %s ", e.Op)
		default:
			unsupported(e)
		}
		t.testExpr(e.Y)
	case *syntax.ParenTest:
		t.str(`\( `)
		t.testExpr(e.X)
		t.str(` \)`)
	case *syntax.UnaryTest:
		switch e.Op {
		case syntax.TsExists,
			syntax.TsRegFile,
			syntax.TsDirect,
			syntax.TsCharSp,
			syntax.TsBlckSp,
			syntax.TsNmPipe,
			syntax.TsSocket,
			syntax.TsSmbLink,
			syntax.TsSticky,
			syntax.TsGIDSet,
			syntax.TsUIDSet,
			syntax.TsGrpOwn,
			syntax.TsUsrOwn,
			syntax.TsRead,
			syntax.TsWrite,
			syntax.TsExec,
			syntax.TsNoEmpty,
			syntax.TsFdTerm,

			syntax.TsEmpStr,
			syntax.TsNempStr,

			syntax.TsNot:
			t.printf("%s ", e.Op)
		default:
			unsupported(e)
		}
		t.testExpr(e.X)
	case *syntax.Word:
		t.word(e, true)
	}
}

func (t *Translator) ifClause(i *syntax.IfClause, elif bool) {
	if elif {
		t.str("else if ")
	} else {
		t.str("if ")
	}
	t.stmts(i.Cond...)
	t.indent()
	t.body(i.Then...)
	t.outdent()

	el := i.Else
	if el != nil && el.ThenPos.IsValid() {
		t.ifClause(el, true)
		return
	}

	if el == nil {
		// comments
	} else {
		t.str("else")
		t.indent()
		t.body(el.Then...)
		t.outdent()
	}

	t.str("end")
}

func (t *Translator) stmts(s ...*syntax.Stmt) {
	for i, s := range s {
		if i > 0 {
			t.str("; ")
		}
		t.stmt(s)
	}
}

func (t *Translator) body(s ...*syntax.Stmt) {
	for i, s := range s {
		if i > 0 {
			t.nl()
		}
		t.stmt(s)
	}
}

func (t *Translator) binaryCmd(c *syntax.BinaryCmd) {
	switch c.Op {
	case syntax.AndStmt:
		t.stmt(c.X)
		t.str(" && ")
		t.stmt(c.Y)
		return
	case syntax.OrStmt:
		t.stmt(c.X)
		t.str(" || ")
		t.stmt(c.Y)
		return
	case syntax.Pipe:
		t.stmt(c.X)
		t.str(" | ")
		t.stmt(c.Y)
		return
	case syntax.PipeAll:
		unsupported(c)
	}
}

var builtins = map[string]string{
	".":     "source",
	"unset": "set -e",
}

func (t *Translator) assign(prefix string, a *syntax.Assign) {
	if a.Append {
		prefix += " -a"
	}
	switch {
	case a.Naked:
		t.printf("set%s %s ", prefix, a.Name.Value)
		t.printf("$%s", a.Name.Value)
	case a.Array != nil:
		t.printf("set%s %s", prefix, a.Name.Value)
		for _, el := range a.Array.Elems {
			if el.Index != nil || el.Value == nil {
				unsupported(a)
			}
			t.str(" ")
			t.word(el.Value, true)
		}
	case a.Value != nil:
		t.printf("set%s %s ", prefix, a.Name.Value)
		t.word(a.Value, true)
	case a.Index != nil:
		unsupported(a)
	}
}

func (t *Translator) callExpr(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		// assignment
		for n, a := range c.Assigns {
			if n > 0 {
				t.str("; ")
			}
			t.assign("", a)
		}
	} else {
		// call
		if len(c.Assigns) > 0 {
			for _, a := range c.Assigns {
				t.printf("%s=", a.Name.Value)
				if a.Value != nil {
					t.word(a.Value, true)
				}
				t.str(" ")
			}
		}

		first := c.Args[0]
		l := first.Lit()
		if replacement, ok := builtins[l]; ok {
			t.str(replacement)
		} else {
			t.word(first, false)
		}

		// TODO: check if we're sourcing/evaling, and insert babelfish
		for _, a := range c.Args[1:] {
			t.str(" ")
			t.word(a, false)
		}
	}
}

func (t *Translator) declClause(c *syntax.DeclClause) {
	var prefix string
	if c.Variant != nil {
		switch c.Variant.Value {
		case "export":
			prefix = " -gx"
		case "local":
			prefix = " -l"
		default:
			unsupported(c)
		}
	}

	for _, a := range c.Args {
		if a.Name == nil {
			unsupported(c)
		}
		t.assign(prefix, a)
	}
}

func (t *Translator) word(w *syntax.Word, mustQuote bool) {
	quote := mustQuote || len(w.Parts) > 1
	for _, part := range w.Parts {
		t.wordPart(part, quote)
	}
}

// wordPart spits out a piece of a Word. The wordparts are placed next to each other, so that they are concatenated into one.
// NOTE: This 'concatentation' is actually a cartesian product.
// This means that every part *needs* to return a list with exactly one item.
// For commands, this means they need to return with just one newline at the end. This means we might need to do something like:
// (begin; <command>;echo;end | string collect)
// To ensure there's always one result.
//
// quote specifies whether this needs to be quoted. This is done so variables and command substitution get expanded.
func (t *Translator) wordPart(wp syntax.WordPart, quoted bool) {
	switch wp := wp.(type) {
	case *syntax.Lit:
		t.str(wp.Value)
	case *syntax.SglQuoted:
		t.escapedString(wp.Value)
	case *syntax.DblQuoted:
		for _, part := range wp.Parts {
			switch part := part.(type) {
			case *syntax.Lit:
				t.escapedString(part.Value)
			default:
				t.wordPart(part, true)
			}
		}
	case *syntax.ParamExp:
		t.paramExp(wp, quoted)
	case *syntax.CmdSubst:
		// Need to ensure there's one element returned from the subst
		if quoted {
			t.str("(echo ")
		}
		t.str("(")
		t.stmts(wp.Stmts...)
		t.str(")")
		if quoted {
			t.str(")")
		}
	case *syntax.ArithmExp:
		t.arithmExpr(wp.X, arithmReturnValue)
	case *syntax.ProcSubst:
		t.str("(")
		t.stmts(wp.Stmts...)
		switch wp.Op {
		case syntax.CmdIn:
			t.str(" | psub")
		case syntax.CmdOut:
			unsupported(wp)
		}
		t.str(")")
	case *syntax.ExtGlob:
		unsupported(wp)
	default:
		unsupported(wp)
	}
}

var specialVariables = map[string]string{
	//"!": "%last", % variables are weird
	"?":        "status",
	"$":        "fish_pid",
	"BASH_PID": "fish_pid",
	"*":        `argv`, // always quote
	"@":        "argv",
	"HOSTNAME": "hostname",
}

// http://tldp.org/LDP/abs/html/internalvariables.html
var literalVariables = map[string]string{
	"UID":    "(id -ru)",
	"EUID":   "(id -u)",
	"GROUPS": "(id -G | string split ' ')",
}

func (t *Translator) paramExp(p *syntax.ParamExp, quoted bool) {
	param := p.Param.Value
	if expr, ok := literalVariables[param]; ok {
		t.str(expr)
		return
	}

	if spec, ok := specialVariables[param]; ok {
		// 🤷
		if param == "*" {
			quoted = true
		}
		param = spec
	}
	switch {
	case p.Excl: // ${!a}
		unsupported(p)
	case p.Length: // ${#a}
		index := p.Index
		switch p.Param.Value {
		case "@", "*":
			index = &syntax.Word{Parts: []syntax.WordPart{p.Param}}
		}
		if index != nil {
			if word, ok := index.(*syntax.Word); ok {
				switch word.Lit() {
				case "@", "*":
					t.printf("(count $%s)", param)
					return
				}
			}
			unsupported(p)
		}
		t.printf(`(string length "$%s")`, param)
	case p.Index != nil: // ${a[i]}, ${a["k"]}
		unsupported(p)
	case p.Width: // ${%a}
		unsupported(p)
	case p.Slice != nil: // ${a:x:y}
		unsupported(p)
	case p.Repl != nil: // ${a/x/y}
		t.str("(string replace ")
		if p.Repl.All {
			t.str("--all ")
		}
		t.word(p.Repl.Orig, true)
		t.str(" ")
		t.word(p.Repl.With, true)
		t.printf(` "$%s")`, param)
	case p.Names != 0: // ${!prefix*} or ${!prefix@}
		unsupported(p)
	case p.Exp != nil:
		// TODO: should probably allow lists to be expanded here
		switch op := p.Exp.Op; op {
		case syntax.AlternateUnsetOrNull:
			t.printf(`(test -n "$%s" && echo `, param)
			t.word(p.Exp.Word, false)
			t.str(" || echo)")
		case syntax.AlternateUnset:
			t.printf(`(set -q %s && echo `, param)
			t.word(p.Exp.Word, false)
			t.str(" || echo)")
		case syntax.DefaultUnsetOrNull:
			t.printf(`(test -n "$%s" && echo "$%s" || echo `, param, param)
			t.word(p.Exp.Word, false)
			t.str(")")
		case syntax.DefaultUnset:
			t.printf(`(set -q %s && echo "$%s" || echo `, param, param)
			t.word(p.Exp.Word, false)
			t.str(")")
		default:
			unsupported(p)
		}
	case p.Short:
		fallthrough
	default:
		if quoted {
			t.printf(`"$%s"`, param)
		} else {
			t.printf(`$%s`, param)
		}
	}
}

var stringReplacer = strings.NewReplacer("\\", "\\\\", "'", "\\'")

func (t *Translator) capture(f func()) {
	oldBuf := t.buf
	newBuf := &bytes.Buffer{}
	t.buf = newBuf
	defer func() {
		t.buf = oldBuf
		t.escapedString(newBuf.String())
	}()
	f()
}

func (t *Translator) escapedString(literal string) {
	t.str("'")
	stringReplacer.WriteString(t.buf, literal)
	t.str("'")
}

func (t *Translator) comment(c *syntax.Comment) {
	t.printf("#%s", c.Text)
	t.nl()
}

func (t *Translator) str(s string) {
	t.buf.WriteString(s)
}

func (t *Translator) printf(format string, arg ...interface{}) {
	fmt.Fprintf(t.buf, format, arg...)
}

func (t *Translator) indent() {
	t.indentLevel++
	t.nl()
}

func (t *Translator) outdent() {
	t.indentLevel--
	t.nl()
}

func (t *Translator) nl() {
	t.buf.WriteRune('\n')
	for i := 0; i < t.indentLevel; i++ {
		t.str("  ")
	}
}
