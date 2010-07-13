package main

import (
	"fmt"
	"bytes"
	"reflect"
	"go/parser"
	"go/ast"
	"go/token"
	"strings"
	"io/ioutil"
	"sort"
	"io"
	"os"
)

// TODO: probably change hand-written string literals processing to a
// "scanner"-based one

func skipSpaces(i int, s string) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func skipToSpace(i int, s string) int {
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}
	return i
}

// convert package name to a nice ident, e.g.: "go/ast" -> "ast"
func identifyPackage(s string) string {
	i := len(s)-1

	// 'i > 0' is correct here, because we should never see '/' at the
	// beginning of the name anyway
	for ; i > 0; i-- {
		if s[i] == '/' {
			break
		}
	}
	if s[i] == '/' {
		return s[i+1:]
	}
	return s
}

func extractPackage(i int, s string) (string, string) {
	pkg := ""

	b := i // first '"'
	i++

	for i < len(s) && s[i] != '"' {
		i++
	}

	if i == len(s) {
		return s, pkg
	}

	e := i // second '"'
	if b+1 != e {
		// wow, we actually have something here
		pkg = s[b+1:e]
	}

	i += 2 // skip to a first symbol after dot
	s = s[0:b] + s[i:] // strip package clause completely

	return s, pkg
}

// returns modified 's' with package stripped from the method and the package name
func extractPackageFromMethod(i int, s string) (string, string) {
	pkg := ""
	for {
		for i < len(s) && s[i] != ')' && s[i] != '"' {
			i++
		}

		if s[i] == ')' || i == len(s) {
			return s, pkg
		}

		b := i // first '"'
		i++

		for i < len(s) && s[i] != ')' && s[i] != '"' {
			i++
		}

		if s[i] == ')' || i == len(s) {
			return s, pkg
		}

		e := i // second '"'
		if b+1 != e {
			// wow, we actually have something here
			pkg = s[b+1:e]
		}

		i += 2 // skip to a first symbol after dot
		s = s[0:b] + s[i:] // strip package clause completely

		i = b
	}
	panic("unreachable")
	return "", ""
}

func expandPackages(s string) string {
	i := 0
	for {
		pkg := ""
		for i < len(s) && s[i] != '"' && s[i] != '=' {
			i++
		}

		if i == len(s) || s[i] == '=' {
			return s
		}

		b := i // first '"'
		i++

		for i < len(s) && !(s[i] == '"' && s[i-1] != '\\') && s[i] != '=' {
			i++
		}

		if i == len(s) || s[i] == '=' {
			return s
		}

		e := i // second '"'
		if s[b-1] == ':' {
			// special case, struct attribute literal, just remove ':'
			s = s[0:b-1] + s[b:]
			i = e
		} else if b+1 != e {
			// wow, we actually have something here
			pkg = identifyPackage(s[b+1:e])
			i++ // skip to a first symbol after second '"'
			s = s[0:b] + pkg + s[i:] // strip package clause completely
			i = b
		} else {
			i += 2
			s = s[0:b] + s[i:]
			i = b
		}

	}
	panic("unreachable")
	return ""
}

func preprocessConstDecl(s string) string {
	i := strings.Index(s, "=")
	if i == -1 {
		return s
	}

	for i < len(s) && !(s[i] >= '0' && s[i] <= '9') && s[i] != '"' && s[i] != '\'' {
		i++
	}

	if i == len(s) || s[i] == '"' || s[i] == '\'' {
		return s
	}

	// ok, we have a digit!
	b := i
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == 'p' || s[i] == '-' || s[i] == '+') {
		i++
	}
	e := i

	return s[0:b] + "0" + s[e:]
}

// feed one definition line from .a file here
// returns:
// 1. a go/parser parsable string representing one Go declaration
// 2. and a package name this declaration belongs to
func processExport(s string) (string, string) {
	i := 0
	pkg := ""

	// skip to a decl type: (type | func | const | var | import)
	i = skipSpaces(i, s)
	if i == len(s) {
		return "", pkg
	}
	b := i
	i = skipToSpace(i, s)
	if i == len(s) {
		return "", pkg
	}
	e := i

	switch s[b:e] {
	case "import":
		// skip import decls, we don't need them
		return "", pkg
	case "const":
		s = preprocessConstDecl(s)
	}
	i++ // skip space after a decl type

	// extract a package this decl belongs to
	switch s[i] {
	case '(':
		s, pkg = extractPackageFromMethod(i, s)
	case '"':
		s, pkg = extractPackage(i, s)
	}

	// make everything parser friendly
	s = strings.Replace(s, "?", "", -1)
	s = expandPackages(s)

	// skip system functions (Init, etc.)
	i = strings.Index(s, "·")
	if i != -1 {
		return "", ""
	}

	return s, pkg
}

func declNames(d ast.Decl) []string {
	var names []string

	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.CONST:
			c := t.Specs[0].(*ast.ValueSpec)
			names = make([]string, len(c.Names))
			for i, name := range c.Names {
				names[i] = name.Name()
			}
		case token.TYPE:
			t := t.Specs[0].(*ast.TypeSpec)
			names = make([]string, 1)
			names[0] = t.Name.Name()
		case token.VAR:
			v := t.Specs[0].(*ast.ValueSpec)
			names = make([]string, len(v.Names))
			for i, name := range v.Names {
				names[i] = name.Name()
			}
		}
	case *ast.FuncDecl:
		names = make([]string, 1)
		names[0] = t.Name.Name()
	}

	return names
}

func declValues(d ast.Decl) []ast.Expr {
	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.VAR:
			v := t.Specs[0].(*ast.ValueSpec)
			if v.Values != nil {
				return v.Values
			}

		}
	}
	return nil
}

func splitDecls(d ast.Decl) []ast.Decl {
	var decls []ast.Decl
	if t, ok := d.(*ast.GenDecl); ok {
		decls = make([]ast.Decl, len(t.Specs))
		for i, s := range t.Specs {
			decl := new(ast.GenDecl)
			*decl = *t
			decl.Specs = make([]ast.Spec, 1)
			decl.Specs[0] = s
			decls[i] = decl
		}
	} else {
		decls = make([]ast.Decl, 1)
		decls[0] = d
	}
	return decls
}

func (self *AutoCompleteContext) processPackage(filename string, uniquename string, pkgname string) {
	if self.cache[filename] {
		self.addAlias(self.m[uniquename].Name, uniquename)
		return
	}
	self.cache[filename] = true

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	s := string(data)

	i := strings.Index(s, "import\n$$\n")
	if i == -1 {
		panic("Can't find the import section in the archive file")
	}
	s = s[i+len("import\n$$\n"):]
	i = strings.Index(s, "$$\n")
	if i == -1 {
		panic("Can't find the end of the import section in the archive file")
	}
	s = s[0:i] // leave only import section

	i = strings.Index(s, "\n")
	if i == -1 {
		panic("Wrong file")
	}

	if pkgname == "" {
		pkgname = s[len("package "):i-1]
	}
	self.addAlias(pkgname, uniquename)

	if self.debuglog != nil {
		fmt.Fprintf(self.debuglog, "parsing package '%s'...\n", pkgname)
	}
	s = s[i+1:]

	internalPackages := make(map[string]*bytes.Buffer)
	for {
		// for each line
		i := strings.Index(s, "\n")
		if i == -1 {
			break
		}
		decl := strings.TrimSpace(s[0:i])
		if len(decl) == 0 {
			s = s[i+1:]
			continue
		}
		decl2, pkg := processExport(decl)
		if len(decl2) == 0 {
			s = s[i+1:]
			continue
		}

		if pkg == "" {
			// local package, use ours name
			pkg = uniquename
		}

		buf := internalPackages[pkg]
		if buf == nil {
			buf = bytes.NewBuffer(make([]byte, 0, 4096))
			internalPackages[pkg] = buf
		}
		buf.WriteString(decl2)
		buf.WriteString("\n")
		s = s[i+1:]
	}
	for key, value := range internalPackages {
		decls, err := parser.ParseDeclList("", value.Bytes(), nil)
		if err != nil {
			panic(fmt.Sprintf("failure in:\n%s\n%s\n", value, err.String()))
		} else {
			if self.debuglog != nil {
				fmt.Fprintf(self.debuglog, "\t%s: OK (ndecls: %d)\n", key, len(decls))
			}
			f := new(ast.File) // fake file
			f.Decls = decls
			ast.FileExports(f)
			localname := ""
			if key == uniquename {
				localname = pkgname
			}
			self.add(key, localname, f.Decls)
		}
	}
}

func prettyPrintTypeExpr(out io.Writer, e ast.Expr) {
	ty := reflect.Typeof(e)
	if false {
		fmt.Fprintf(out, "%s\n", ty.String())
	}
	switch t := e.(type) {
	case *ast.StarExpr:
		fmt.Fprintf(out, "*")
		prettyPrintTypeExpr(out, t.X)
	case *ast.Ident:
		fmt.Fprintf(out, t.Name())
	case *ast.ArrayType:
		fmt.Fprintf(out, "[]")
		prettyPrintTypeExpr(out, t.Elt)
	case *ast.SelectorExpr:
		prettyPrintTypeExpr(out, t.X)
		fmt.Fprintf(out, ".%s", t.Sel.Name())
	case *ast.FuncType:
		fmt.Fprintf(out, "func(")
		prettyPrintFuncFieldList(out, t.Params)
		fmt.Fprintf(out, ")")

		buf := bytes.NewBuffer(make([]byte, 0, 256))
		nresults := prettyPrintFuncFieldList(buf, t.Results)
		if nresults > 0 {
			results := buf.String()
			if strings.Index(results, " ") != -1 {
				results = "(" + results + ")"
			}
			fmt.Fprintf(out, " %s", results)
		}
	case *ast.MapType:
		fmt.Fprintf(out, "map[")
		prettyPrintTypeExpr(out, t.Key)
		fmt.Fprintf(out, "]")
		prettyPrintTypeExpr(out, t.Value)
	case *ast.InterfaceType:
		fmt.Fprintf(out, "interface{}")
	case *ast.Ellipsis:
		fmt.Fprintf(out, "...")
		prettyPrintTypeExpr(out, t.Elt)
	case *ast.StructType:
		fmt.Fprintf(out, "struct")
	case *ast.CallExpr:
		prettyPrintTypeExpr(out, t.Fun)
	case *ast.ChanType:
		fmt.Fprintf(out, "chan ")
		prettyPrintTypeExpr(out, t.Value)
	default:
		fmt.Fprintf(out, "\n[!!] unknown type: %s\n", ty.String())
	}
}

func prettyPrintFuncFieldList(out io.Writer, f *ast.FieldList) int {
	count := 0
	if f == nil {
		return count
	}
	for i, field := range f.List {
		// names
		if field.Names != nil {
			for j, name := range field.Names {
				fmt.Fprintf(out, "%s", name.Name())
				if j != len(field.Names)-1 {
					fmt.Fprintf(out, ", ")
				}
				count++
			}
			fmt.Fprintf(out, " ")
		} else {
			count++
		}

		// type
		prettyPrintTypeExpr(out, field.Type)

		// ,
		if i != len(f.List)-1 {
			fmt.Fprintf(out, ", ")
		}
	}
	return count
}

func startsWith(s, prefix string) bool {
	if len(s) >= len(prefix) && s[0:len(prefix)] == prefix {
		return true
	}
	return false
}

func prettyPrintDecl(out io.Writer, d ast.Decl, p string) {
	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.CONST:
			for _, spec := range t.Specs {
				c := spec.(*ast.ValueSpec)
				for _, name := range c.Names {
					if p != "" && !startsWith(name.Name(), p) {
						continue
					}
					fmt.Fprintf(out, "const %s\n", name.Name())
				}
			}
		case token.TYPE:
			for _, spec := range t.Specs {
				t := spec.(*ast.TypeSpec)
				if p != "" && !startsWith(t.Name.Name(), p) {
					continue
				}
				fmt.Fprintf(out, "type %s\n", t.Name.Name())
			}
		case token.VAR:
			for _, spec := range t.Specs {
				v := spec.(*ast.ValueSpec)
				for _, name := range v.Names {
					if p != "" && !startsWith(name.Name(), p) {
						continue
					}
					fmt.Fprintf(out, "var %s\n", name.Name())
				}
			}
		}
	case *ast.FuncDecl:
		if t.Recv != nil {
			//XXX: skip method, temporary
			break
		}
		if p != "" && !startsWith(t.Name.Name(), p) {
			break
		}
		fmt.Fprintf(out, "func %s(", t.Name.Name())
		prettyPrintFuncFieldList(out, t.Type.Params)
		fmt.Fprintf(out, ")")

		buf := bytes.NewBuffer(make([]byte, 0, 256))
		nresults := prettyPrintFuncFieldList(buf, t.Type.Results)
		if nresults > 0 {
			results := buf.String()
			if strings.Index(results, " ") != -1 {
				results = "(" + results + ")"
			}
			fmt.Fprintf(out, " %s", results)
		}
		fmt.Fprintf(out, "\n")
	}
}

func prettyPrintAutoCompleteDecl(out io.Writer, d ast.Decl, p string) {
	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.CONST:
			for _, spec := range t.Specs {
				c := spec.(*ast.ValueSpec)
				for _, name := range c.Names {
					if p != "" && !startsWith(name.Name(), p) {
						continue
					}
					fmt.Fprintf(out, "%s\n", name.Name()[len(p):])
				}
			}
		case token.TYPE:
			for _, spec := range t.Specs {
				t := spec.(*ast.TypeSpec)
				if p != "" && !startsWith(t.Name.Name(), p) {
					continue
				}
				fmt.Fprintf(out, "%s\n", t.Name.Name()[len(p):])
			}
		case token.VAR:
			for _, spec := range t.Specs {
				v := spec.(*ast.ValueSpec)
				for _, name := range v.Names {
					if p != "" && !startsWith(name.Name(), p) {
						continue
					}
					fmt.Fprintf(out, "%s\n", name.Name()[len(p):])
				}
			}
		default:
			fmt.Fprintf(out, "[!!] STUB\n")
		}
	case *ast.FuncDecl:
		if t.Recv != nil {
			//XXX: skip method, temporary
			break
		}
		if p != "" && !startsWith(t.Name.Name(), p) {
			break
		}
		fmt.Fprintf(out, "%s(\n", t.Name.Name()[len(p):])
	}
}

func findFile(imp string) string {
	goroot := os.Getenv("GOROOT")
	goarch := os.Getenv("GOARCH")
	goos := os.Getenv("GOOS")

	return fmt.Sprintf("%s/pkg/%s_%s/%s.a", goroot, goos, goarch, imp)
}

func pathAndAlias(imp *ast.ImportSpec) (string, string) {
	path := string(imp.Path.Value)
	alias := ""
	if imp.Name != nil {
		alias = imp.Name.Name()
	}
	path = path[1:len(path)-1]
	return path, alias
}

func (self *AutoCompleteContext) processImportSpec(imp *ast.ImportSpec) {
	path, alias := pathAndAlias(imp)
	self.processPackage(findFile(path), path, alias)
}

func (self *AutoCompleteContext) processDecl(decl ast.Decl) {
	switch t := decl.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.IMPORT:
			for _, spec := range t.Specs {
				imp, ok := spec.(*ast.ImportSpec)
				if !ok {
					panic("Fail")
				}
				self.processImportSpec(imp)
			}
		}
	}

	decls := splitDecls(decl)
	for _, decl := range decls {
		names := declNames(decl)
		values := declValues(decl)

		for i, name := range names {
			var value ast.Expr = nil
			valueindex := -1
			if values != nil {
				if len(values) > 1 {
					value = values[i]
				} else {
					value = values[0]
					valueindex = i
				}
			}

			d := astDeclToDecl(name, decl, value, valueindex)
			if d == nil {
				continue
			}

			methodof := MethodOf(decl)
			if methodof != "" {
				decl, ok := self.l[methodof]
				if ok {
					decl.AddChild(d)
				} else {
					decl = newDecl(methodof)
					self.l[methodof] = decl
					decl.AddChild(d)
				}
			} else {
				decl, ok := self.l[d.Name]
				if ok {
					decl.ApplyDecl(d)
				} else {
					self.l[d.Name] = d
				}
			}
		}
	}
}

func (self *AutoCompleteContext) processData(data []byte) {
	// drop namespace and locals
	self.l = make(map[string]*Decl)
	self.cfns = make(map[string]string)

	// ignore errors here
	file, _ := parser.ParseFile("", data, nil, 0)
	for _, decl := range file.Decls {
		self.processDecl(decl)
	}
}

type AutoCompleteContext struct {
	m map[string]*Decl // all modules
	l map[string]*Decl // locals

	// current file namespace (used for modules):
	// alias name ->
	//	unique package name
	cfns map[string]string

	cache map[string]bool // stupid, temporary

	debuglog io.Writer
}

func NewAutoCompleteContext() *AutoCompleteContext {
	self := new(AutoCompleteContext)
	self.m = make(map[string]*Decl)
	self.l = make(map[string]*Decl)
	self.cfns = make(map[string]string)
	self.cache = make(map[string]bool)
	return self
}

func (self *AutoCompleteContext) add(globalname, localname string, decls []ast.Decl) {
	if self.m[globalname] == nil {
		self.m[globalname] = NewDecl(localname, DECL_MODULE)
	}

	if self.m[globalname].Name == "" && localname != "" {
		self.m[globalname].Name = localname
	}

	for _, decl := range decls {
		decls := splitDecls(decl)
		for _, decl := range decls {
			names := declNames(decl)
			values := declValues(decl)

			for i, name := range names {
				var value ast.Expr = nil
				valueindex := -1
				if values != nil {
					if len(values) > 1 {
						value = values[i]
					} else {
						value = values[0]
						valueindex = i
					}
				}

				d := astDeclToDecl(name, decl, value, valueindex)
				if d == nil {
					continue
				}

				methodof := MethodOf(decl)
				if methodof != "" {
					if !ast.IsExported(methodof) {
						continue
					}
					decl := self.m[globalname].FindChild(methodof)
					if decl != nil {
						decl.AddChild(d)
					} else {
						decl = newDecl(methodof)
						self.m[globalname].AddChild(decl)
						decl.AddChild(d)
					}
				} else {
					decl := self.m[globalname].FindChild(d.Name)
					if decl != nil {
						decl.ApplyDecl(d)
					} else {
						self.m[globalname].AddChild(d)
					}
				}
			}
		}
	}
}

func (self *AutoCompleteContext) addAlias(alias string, globalname string) {
	self.cfns[alias] = globalname
}

func (self *AutoCompleteContext) findDecl(name string) *Decl {
	realname, ok := self.cfns[name]
	if ok {
		d, ok := self.m[realname]
		if ok {
			return d
		}
	}

	d, ok := self.l[name]
	if ok {
		return d
	}
	return nil
}

//-------------------------------------------------------------------------
// Sort interface for TwoStringArrays
//-------------------------------------------------------------------------

type TwoStringArrays struct {
	first []string
	second []string
}

func (self TwoStringArrays) Len() int {
	return len(self.first)
}

func (self TwoStringArrays) Less(i, j int) bool {
	return self.first[i] < self.first[j]
}

func (self TwoStringArrays) Swap(i, j int) {
	self.first[i], self.first[j] = self.first[j], self.first[i]
	self.second[i], self.second[j] = self.second[j], self.second[i]
}

//-------------------------------------------------------------------------

func (self *AutoCompleteContext) Apropos(file []byte, apropos string) ([]string, []string) {
	self.processData(file)

	buf := bytes.NewBuffer(make([]byte, 0, 4096))
	buf2 := bytes.NewBuffer(make([]byte, 0, 4096))

	parts := strings.Split(apropos, ".", 2)
	switch len(parts) {
	case 1:
		// propose modules
		for _, value := range self.cfns {
			if decl, ok := self.m[value]; ok {
				decl.PrettyPrint(buf, parts[0])
				decl.PrettyPrintAutoComplete(buf2, parts[0])
			}
		}
		// and locals
		for _, value := range self.l {
			value.InferType(self)
			value.PrettyPrint(buf, parts[0])
			value.PrettyPrintAutoComplete(buf2, parts[0])
		}
	case 2:
		if topdecl := self.findDecl(parts[0]); topdecl != nil {
			switch topdecl.Class {
			case DECL_MODULE:
				for _, decl := range topdecl.Children {
					decl.PrettyPrint(buf, parts[1])
					decl.PrettyPrintAutoComplete(buf2, parts[1])
				}
			case DECL_VAR:
				it := topdecl.InferType(self)
				name := typeName(it)
				if typdecl := self.findDecl(name); typdecl != nil {
					for _, decl := range typdecl.Children {
						decl.PrettyPrint(buf, parts[1])
						decl.PrettyPrintAutoComplete(buf2, parts[1])
					}
				}
			}
		}
	}

	if buf.Len() == 0 || buf2.Len() == 0 {
		return nil, nil
	}

	var pair TwoStringArrays
	pair.first = strings.Split(buf.String()[0:buf.Len()-1], "\n", -1)
	pair.second = strings.Split(buf2.String()[0:buf2.Len()-1], "\n", -1)
	sort.Sort(pair)
	return pair.first, pair.second
}

func (self *AutoCompleteContext) Status() string {
	buf := bytes.NewBuffer(make([]byte, 0, 4096))
	fmt.Fprintf(buf, "Number of top level packages: %d\n", len(self.m))
	if len(self.m) > 0 {
		fmt.Fprintf(buf, "Listing packages: ")
		i := 0
		for key, _ := range self.m {
			fmt.Fprintf(buf, "'%s'", key)
			if i != len(self.m)-1 {
				fmt.Fprintf(buf, ", ")
			}
			i++
		}
		fmt.Fprintf(buf, "\n")
	}
	return buf.String()
}
