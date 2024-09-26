package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/buildutil"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

var testdata_src1 = `
package main

import "fmt"

func main() {
	// 変数宣言
	var a []int
	var b map[string]int
	var c chan int

	// 関数の使用
	a = append(a, 1)
	b = make(map[string]int)
	b["key"] = 2
	c = make(chan int)

	go func() {
		c <- 3
	}()

	fmt.Println(a)
	fmt.Println(b)
	fmt.Println(<-c)

	// MyStructの使用
	nested := MyStruct{
		field1: 1,
		field2: "example",
		nestedStruct: &MyStruct{
			field1: 2,
			field2: "nested",
		},
	}
	fmt.Println(nested)
}

// 構造体定義
type MyStruct struct {
	field1       int
	field2       string
	nestedStruct *MyStruct
}

// インターフェース定義
type MyInterface interface {
	Method1() int
	Method2(string) bool
}

// インターフェースの実装
type MyImplementation struct{}

func (mi MyImplementation) Method1() int {
	return 1
}

func (mi MyImplementation) Method2(s string) bool {
	return s == "true"
}

// MyImplementationの使用
func useInterface(mi MyInterface) {
	fmt.Println(mi.Method1())
	fmt.Println(mi.Method2("true"))
}

func init() {
	// インターフェースの実装を使う
	impl := MyImplementation{}
	useInterface(impl)
}
`

var testdata_src_2_main = `
package main

import (
	"example"
	"fmt"
)

func main() {
	// 変数宣言
	var a []int
	var b map[string]int
	var c chan int

	// 関数の使用
	a = append(a, 1)
	b = make(map[string]int)
	b["key"] = 2
	c = make(chan int)

  // main パッケージの関数の使用
  hello()

	// インターフェースの実装を使う
	impl := MyImplementation{}
	useInterface(impl)

	go func() {
		c <- 3
	}()

	fmt.Println(a)
	fmt.Println(b)
	fmt.Println(<-c)

	// MyStructの使用
	nested := MyStruct{
		field1: 1,
		field2: "example",
		nestedStruct: &MyStruct{
			field1: 2,
			field2: "nested",
		},
	}
	fmt.Println(nested)
  nested.Method1()
  nested.Method2("string")

	// example パッケージの関数と型の使用
	example.Example()

	exampleStruct := example.AnotherStruct{AnotherField: 10}
	fmt.Println(exampleStruct)

	var impl example.AnotherInterface = example.AnotherImplementation{}
	fmt.Println(impl.AnotherMethod())
}

// 構造体定義
type MyStruct struct {
	field1       int
	field2       string
	nestedStruct *MyStruct
}

// MyStructにメソッドを追加してMyInterfaceを実装
func (ms MyStruct) Method1() int {
	return ms.field1
}

func (ms MyStruct) Method2(s string) bool {
	return ms.field2 == s
}

// インターフェース定義
type MyInterface interface {
	Method1() int
	Method2(string) bool
}

// インターフェースの実装
type MyImplementation struct{}

func (mi MyImplementation) Method1() int {
	return 1
}

func (mi MyImplementation) Method2(s string) bool {
	return s == "true"
}

// MyImplementationの使用
func useInterface(mi MyInterface) {
	fmt.Println(mi.Method1())
	fmt.Println(mi.Method2("true"))
}

func hello() {
  fmt.Println("Hello")
}
`

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

var testdata_src_2_example = `
package example

import "fmt"

func Example() {
	fmt.Println("This is an example function.")
}

// AnotherStructの定義
type AnotherStruct struct {
	AnotherField int
}

// AnotherInterfaceの定義
type AnotherInterface interface {
	AnotherMethod() string
}

// AnotherImplementationの実装
type AnotherImplementation struct{}

func (ai AnotherImplementation) AnotherMethod() string {
	return "AnotherMethod called"
}`

func jsonMarshal(v interface{}) string {
	str, _ := json.Marshal(v)
	return string(str)
}

func TestFindMainFunction(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", testdata_src1, parser.AllErrors)
	if err != nil {
		log.Fatalf("Failed to parse file: %v", err)
	}

	// ASTを巡回してmain関数を探す
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, isFunc := n.(*ast.FuncDecl); isFunc && fn.Name.Name == "main" {
			// main関数の本体を出力
			log.Println("Found main function:")
			ast.Print(fset, fn.Body)
			return false // main関数が見つかったので巡回を終了
		}
		return true
	})
}

func TestUsedFromMainFunction(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", testdata_src1, parser.AllErrors)
	if err != nil {
		log.Fatalf("Failed to parse file: %v", err)
	}

	// main関数を探す
	var mainFn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, isFunc := n.(*ast.FuncDecl); isFunc && fn.Name.Name == "main" {
			mainFn = fn
			return false // main関数が見つかったので巡回を終了
		}
		return true
	})

	// main関数の中で使用されている識別子を出力
	log.Println("Identifiers used from main function:")
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		if ident, isIdent := n.(*ast.Ident); isIdent {
			log.Println(ident.Name)
		}
		return true
	})

	// main関数の中で使用されているセレクタを出力
	log.Println("Selectors used from main function:")
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		if selector, isSelector := n.(*ast.SelectorExpr); isSelector {
			log.Println(selector.X, selector.Sel)
		}
		return true
	})
}

func TestFindFunctionsAndTypes(t *testing.T) {
	sources := []string{testdata_src_2_main, testdata_src_2_example}

	for i, src := range sources {
		t.Run(fmt.Sprintf("Source_%d", i+1), func(t *testing.T) {
			fset := token.NewFileSet()
			node, err := parser.ParseFile(fset, "", src, parser.AllErrors)
			if err != nil {
				log.Fatalf("Failed to parse file: %v", err)
			}

			// ASTを巡回して関数と型を探す
			ast.Inspect(node, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncDecl:
					log.Printf("Found function: %s\n", x.Name.Name)
				case *ast.GenDecl:
					for _, spec := range x.Specs {
						switch spec := spec.(type) {
						case *ast.TypeSpec:
							log.Printf("Found type: %s\n", spec.Name.Name)
							switch spec.Type.(type) {
							case *ast.StructType:
								log.Printf("Type %s is a struct\n", spec.Name.Name)
							case *ast.InterfaceType:
								log.Printf("Type %s is an interface\n", spec.Name.Name)
							}
						}
					}
				}
				return true
			})
		})
	}
}

func TestUsedFromMainFunctionSrc2(t *testing.T) {
	sourceMain := testdata_src_2_main

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", sourceMain, parser.AllErrors)
	if err != nil {
		log.Fatalf("Failed to parse file: %v", err)
	}

	// main関数を探す
	var mainFn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, isFunc := n.(*ast.FuncDecl); isFunc && fn.Name.Name == "main" {
			mainFn = fn
			return false // main関数が見つかったので巡回を終了
		}
		return true
	})

	if mainFn == nil {
		t.Fatalf("main function not found, src: %s", sourceMain)
	}

	// CallExpr struct {
	// 	Fun      Expr      // function expression
	// 	Lparen   token.Pos // position of "("
	// 	Args     []Expr    // function arguments; or nil
	// 	Ellipsis token.Pos // position of "..." (token.NoPos if there is no "...")
	// 	Rparen   token.Pos // position of ")"
	// }
	// TODO: callExpr.Args に渡された引数も取得

	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			switch fun := callExpr.Fun.(type) {
			case *ast.Ident:
				// main関数の中で使用されている関数を出力
				// log.Println("identifier", jsonMarshal(fun))
				log.Printf("identifier %s", fun.Name)
			case *ast.SelectorExpr:
				// main関数の中で使用されているセレクタを出力
				if ident, ok := fun.X.(*ast.Ident); ok {
					// log.Println("selector ", jsonMarshal(ident))
					log.Printf("selector %s %s", ident.Name, fun.Sel.Name)
				}

			}
		}
		return true
	})

	// TODO: example パッケージが関数内で使用されている場合、src_2_example も解析する

	// pos := files[0].Package
	// name := files[0].Name

	// file := &ast.File{
	// 	Package: pos,
	// 	Name:    name,
	// 	Decls:   decls,
	// }

	// Selectors used from main function:
	// append
	// example AnotherImplementation
	// example AnotherInterface
	// example AnotherStruct
	// example Example
	// fmt Println
	// hello
	// impl AnotherMethod
	// make
	// nested Method1
	// nested Method2
	// useInterface

	// TODO:
	// * package 名なのか instance 名なのか判別
	// * hello, useInterface のような main pacakge ないの関数は main hello と出力
	// * append, make のような build-in 関数は build-in append のように出力
	// * nested Method1, nested Method2 は main MyStruct
}

func TestExtractVariableValue(t *testing.T) {
	src := `package main
const variable = "value"
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GenDecl:
			for _, spec := range x.Specs {
				switch s := spec.(type) {
				case *ast.ImportSpec:
					log.Printf("Import %s\n", s.Path.Value)
				case *ast.TypeSpec:
					log.Printf("Type %s\n", s.Name.Name)
				case *ast.ValueSpec:
					log.Printf("variableName %s, value %s\n", s.Names[0].Name, s.Names[0].Obj.Decl.(*ast.ValueSpec).Values[0].(*ast.BasicLit).Value)
				}
			}
		}
		return true
	})
}

func TestIdentIsPackageFunctionOrInstance(t *testing.T) {
	src := `package main
import (
  "fmt"
)

type MyStruct struct {
  field1       int
}

func (ms MyStruct) Method1() int {
  return ms.field1
}

func main() {
  fmt.Println("Hello, world!")
  nested := MyStruct{
    field1: 1,
  }
  fmt.Println(nested)
  nested.Method1()
}
`

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(node, func(n ast.Node) bool {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			switch fun := callExpr.Fun.(type) {
			case *ast.Ident:
				log.Printf("identifier %s", fun.Name)
			case *ast.SelectorExpr:
				if ident, ok := fun.X.(*ast.Ident); ok {
					log.Printf("selector %s %s", ident.Name, fun.Sel.Name)
				}
			}
		}
		return true
	})
}

func TestIdentIsPackageFunctionOrInstance2(t *testing.T) {
	src := `package main
import (
  "fmt"
)

type MyStruct struct {
  field1       int
}

func (ms MyStruct) Method1() int {
  return ms.field1
}

func main() {
  fmt.Println("Hello, world!")
  nested := MyStruct{
    field1: 1,
  }
  fmt.Println(nested)
  nested.Method1()
}
`

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}

	_, err = conf.Check("", fset, []*ast.File{node}, info)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(node, func(n ast.Node) bool {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			switch fun := callExpr.Fun.(type) {
			case *ast.Ident:
				obj := info.ObjectOf(fun)
				if obj != nil {
					log.Printf("identifier %s (type: %T)", fun.Name, obj)
				} else {
					log.Printf("identifier %s (type: unknown)", fun.Name)
				}
			case *ast.SelectorExpr:
				if ident, ok := fun.X.(*ast.Ident); ok {
					obj := info.ObjectOf(ident)
					if obj != nil {
						switch obj.(type) {
						case *types.PkgName:
							log.Printf("selector %s (package) %s", ident.Name, fun.Sel.Name)
						default:
							log.Printf("selector %s (instance) %s", ident.Name, fun.Sel.Name)
						}
					} else {
						log.Printf("selector %s (unknown) %s", ident.Name, fun.Sel.Name)
					}
				}
			}
		}
		return true
	})
}

func TestLookUpStructTypeEmbeded(t *testing.T) {
	src := `package main

import (
	"fmt"
)

type MyStructA struct {
	MyStructB
}

type MyStructB struct {
	field1 int
}

func (ms MyStructA) Method1() int {
	return ms.field1
}
`

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	astFiles := []*ast.File{node}

	pkg, _ := conf.Check("main", fset, astFiles, nil)
	obj := pkg.Scope().Lookup("MyStructA")

	fmt.Println(obj.Type().String()) // MyStructA

	typeUnderlying := obj.Type().Underlying()
	fmt.Println(typeUnderlying)

	strct := typeUnderlying.(*types.Struct)
	field := strct.Field(0)
	fmt.Println(field.Name(), field.Embedded()) // MyStructB true
}

func TestLookUpStructTypeEmbeded2(t *testing.T) {
	src1 := `package main

import (
	"fmt"
)

type MyStructA struct {
	MyStructB
}

func (ms MyStructA) Method1() int {
	return ms.field1
}
`
	src2 := `package main

type MyStructB struct {
	field1 int
}
`

	fset := token.NewFileSet()
	file1, err := parser.ParseFile(fset, "", src1, 0)
	if err != nil {
		log.Fatal(err)
	}
	file2, err := parser.ParseFile(fset, "", src2, 0)
	if err != nil {
		log.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	astFiles := []*ast.File{file1, file2}

	pkg, _ := conf.Check("main", fset, astFiles, nil)
	obj := pkg.Scope().Lookup("MyStructA")

	fmt.Println(obj.Type().String()) // MyStructA

	typeUnderlying := obj.Type().Underlying()
	fmt.Println(typeUnderlying)

	strct := typeUnderlying.(*types.Struct)
	field := strct.Field(0)
	fmt.Println(field.Name(), field.Embedded()) // MyStructB true
}

func TestLookUpStructTypeEmbeded3(t *testing.T) {
	src1 := `package main

import (
	"fmt"
	"example"
)

type MyStructA struct {
	example.MyStructB
}

func (ms MyStructA) Method1() int {
	return ms.MyStructB.field1
}

func main() {
	a := &MyStructA{}
	fmt.Println(a.Method1())
}
`

	src2 := `package example

	type MyStructB struct {
	field1 int
	}
	`

	fset := token.NewFileSet()

	file1, err := parser.ParseFile(fset, "src1.go", src1, 0)
	if err != nil {
		log.Fatal("Error parsing src1: ", err)
	}

	file2, err := parser.ParseFile(fset, "src2.go", src2, 0)
	if err != nil {
		log.Fatal("Error parsing src2: ", err)
	}

	conf := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedDeps | packages.NeedImports | packages.NeedTypesInfo,
	}

	pkgs, err := packages.Load(conf, file1.Name.Name, file2.Name.Name)
	if err != nil {
		log.Fatalf("Failed to load packages: %v", err)
	}

	targetPkgName := "main"
	var targetPkg *packages.Package
	for _, pkg := range pkgs {
		fmt.Println(pkg.Syntax)
		if pkg.ID == targetPkgName {
			targetPkg = pkg
			break
		}
	}
	if targetPkg == nil {
		log.Printf("target package %s not found", targetPkgName)
	}

	// Why syntax is empty ?
	fmt.Println(targetPkg.Syntax)
}

func TestLookUpStructTypeEmbeded4(t *testing.T) {
	src1 := `package main

import (
	"fmt"
	"example"
)

type MyStructA struct {
	example.MyStructB
}

type MyStructC struct {
	field int
}

func (ms MyStructA) Method1() int {
	return ms.MyStructB.field1
}

func main() {
	a := &MyStructA{}
	fmt.Println(a.Method1())
  c := &MyStructC{
    field: 1,
  }
  fmt.Println(c.field)
}
`

	src2 := `package example

	type MyStructB struct {
	field1 int
	}
	`

	fset := token.NewFileSet()

	file1, err := parser.ParseFile(fset, "src1.go", src1, 0)
	if err != nil {
		log.Fatal("Error parsing src1: ", err)
	}

	file2, err := parser.ParseFile(fset, "src2.go", src2, 0)
	if err != nil {
		log.Fatal("Error parsing src2: ", err)
	}

	targetPkgName := "main"
	var targetFile *ast.File
	astFiles := []*ast.File{file1, file2}
	for _, file := range astFiles {
		if file.Name.Name == targetPkgName {
			targetFile = file
		}
	}
	astutil.Apply(targetFile, func(c *astutil.Cursor) bool {
		switch node := c.Node().(type) {
		case *ast.CompositeLit:
			fmt.Println("composite lit", node)
		case *ast.KeyValueExpr:
			fmt.Println("keyValue lit", node)
		}
		return true
	}, nil)
}

func TestInspectFunctionReferences(t *testing.T) {
	src := `package main

type A struct{
  base int
}

type B struct {
  base int
}

type C int

func (a *A) calc1(v int) int {
  return add(v, 1)
}

func (b B) calc1(v int) int {
  return add(v, 1)
}

func (c C) calc1(v int) int {
  return add(v, 1)
}

func calc1(a int) int {
  return add(a, 1)
}

func calc2(a int) int {
  return add(a, 2)
}

func add(a, b int) int {
  return a + b
}

func main() {
  add(calc1(1), calc2(2))
  a := A{base: 10}
  a.calc1(3)

  b := B{base: 10}
  b.calc1(4)

	var c C
	c.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "add" {
					if fd.Recv != nil {
						var recvName string
						switch e := fd.Recv.List[0].Type.(type) {
						case *ast.StarExpr:
							if ident, ok := e.X.(*ast.Ident); ok {
								recvName = "*" + ident.Name
							}
						case *ast.Ident:
							recvName = e.Name
						}
						log.Printf("Recv '%s', Function '%s' calls add\n", recvName, fd.Name)
					} else {
						log.Printf("Function '%s' calls add\n", fd.Name)
					}
				}
			}

			return true
		})

		return true
	})
}

func TestInspectFunctionReferences2(t *testing.T) {
	src := `package main

type Calculator struct {}

func NewCalculator() *Calculator {
  return &Calculator{}
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

func (c *Calculator) sub(a, b int) int {
  return a - b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA() *A {
  return &A{calculator: NewCalculator()}
}

func (a *A) calc1(v int) int {
  return a.calculator.add(v, base)
}

func main() {
  ai := NewA()
  ai.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			inspectFunctionTypeAndName := func(fd *ast.FuncDecl) {
				if fd.Recv != nil {
					var recvName string
					switch e := fd.Recv.List[0].Type.(type) {
					case *ast.StarExpr:
						if ident, ok := e.X.(*ast.Ident); ok {
							recvName = "*" + ident.Name
						}
					case *ast.Ident:
						recvName = e.Name
					}
					log.Printf("Recv '%s', Function '%s' calls add\n", recvName, fd.Name)
				} else {
					log.Printf("Function '%s' calls add\n", fd.Name)
				}
			}

			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "add" {
					inspectFunctionTypeAndName(fd)
				}
			case *ast.SelectorExpr:
				if fun.Sel.Name == "add" {
					inspectFunctionTypeAndName(fd)
				}
				if ident, ok := fun.X.(*ast.Ident); ok {
					log.Printf("Selector '%s', Function '%s' calls add\n", ident.Name, fd.Name)
				}
			}

			return true
		})

		return true
	})
}

func TestReplaceFmt(t *testing.T) {
	src := `package main
import (
  "fmt"
)

func main() {
  a := 1
  fmt.Println(a)

  b := "hello"
  fmt.Println(b)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
	}

	conf := &types.Config{
		Importer: importer.Default(),
	}

	_, err = conf.Check("main", fset, []*ast.File{file}, info)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if fd.Name == nil {
			return true
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := fun.X.(*ast.Ident); ok {
					if ident.Name == "fmt" && fun.Sel.Name == "Println" {
						if len(call.Args) == 1 {
							arg := call.Args[0]
							if tv, ok := info.Types[arg]; ok {
								var newFormat string
								switch tv.Type.String() {
								case "int":
									newFormat = "\"%d\""
								case "string":
									newFormat = "\"%s\""
								default:
									return true
								}

								newCall := &ast.CallExpr{
									Fun: &ast.SelectorExpr{
										X:   ast.NewIdent("fmt"),
										Sel: ast.NewIdent("Printf"),
									},
									Args: []ast.Expr{
										ast.NewIdent(newFormat),
										arg,
									},
								}

								*call = *newCall
							}
						}
					}
				}
			}
			return true
		})
		return true
	})

	ast.Inspect(file, func(n ast.Node) bool {
		return true
	})

	if err := format.Node(os.Stdout, fset, file); err != nil {
		log.Fatal(err)
	}
}

func TestReplaceFmt2(t *testing.T) {
	src := `package main

import (
  "fmt"
)

type A struct{}

func NewA() *A {
  return &A{}
}

func (a *A) calc1(v int) int {
  return a.calculator.add(v, a.base)
}

func main() {
  ai := NewA()
  calced := ai.calc1(1)
  fmt.Println(calced)
  fmt.Println("Hello, world!")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if fd.Name == nil {
			return true
		}

		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := fun.X.(*ast.Ident); ok {
					if ident.Name == "fmt" && fun.Sel.Name == "Println" {
						arg, ok := call.Args[0].(*ast.Ident)
						if ok && arg.Name == "calced" {
							args := []ast.Expr{&ast.BasicLit{
								Kind:  token.STRING,
								Value: `"%d"`,
							}, arg}
							call.Fun = &ast.SelectorExpr{
								X:   ast.NewIdent("fmt"),
								Sel: ast.NewIdent("Printf"),
							}
							call.Args = args
						}
					}
					log.Printf("1 Recv '%s', Function '%s'\n", ident.Name, fun.Sel.Name)
				}

				if fun1, ok := fun.X.(*ast.SelectorExpr); ok {
					if ident, ok := fun1.X.(*ast.Ident); ok {
						log.Printf("Recv '%s', Function '%s'\n", ident.Name, fun1.Sel.Name) // Recv 'a', Function 'calculator'
					}
				}
			}
			if fun, ok := call.Fun.(*ast.Ident); ok {
				log.Printf("Function '%s'\n", fun.Name)
			}
			return true
		})
		return true
	})

	ast.Inspect(file, func(n ast.Node) bool {
		return true
	})

	if err := format.Node(os.Stdout, fset, file); err != nil {
		log.Fatal(err)
	}
}

// SelectorExpr を再帰的にトラバースして、レシーバ部分を解析。
func traverseSelectorExpr(se *ast.SelectorExpr) {
	if sel, ok := se.X.(*ast.SelectorExpr); ok {
		traverseSelectorExpr(sel)
		log.Printf("Intermediate '%s'\n", sel.Sel.Name)
	} else if ident, ok := se.X.(*ast.Ident); ok {
		log.Printf("Base '%s'\n", ident.Name)
	}
	log.Printf("Function '%s'\n", se.Sel.Name)
}

// SelectorExpr の最も内側の識別子が構造体フィールドであれば、そのフィールドの型を確認。
func traverseSelectorExpr2(se *ast.SelectorExpr, info *types.Info, fset *token.FileSet) {
	if sel, ok := se.X.(*ast.SelectorExpr); ok {
		traverseSelectorExpr2(sel, info, fset)
		log.Printf("Intermediate '%s'\n", sel.Sel.Name)
	} else if ident, ok := se.X.(*ast.Ident); ok {
		log.Printf("Ident '%s' found, looking up type info...\n", ident.Name)
		if obj, ok := info.Uses[ident]; ok {
			log.Printf("Object '%s' found with type '%s'\n", ident.Name, obj.Type())
		} else {
			log.Printf("Object '%s' not found in type info\n", ident.Name)
		}
	}
	log.Printf("Function '%s'\n", se.Sel.Name)
}

// SelectorExpr の内側の識別子が構造体フィールドであれば、そのフィールドの型も確認。
func traverseSelectorExpr3(se *ast.SelectorExpr, info *types.Info, fset *token.FileSet) {
	if sel, ok := se.X.(*ast.SelectorExpr); ok {
		// ここに caluculator を解析する処理を記述する
		if obj, ok := info.Selections[sel]; ok {
			log.Printf("Intermediate object '%s' found with type '%s'\n", sel.Sel.Name, obj.Type())
		} else {
			log.Printf("Intermediate object '%s' not found in type info\n", sel.Sel.Name)
		}
		traverseSelectorExpr3(sel, info, fset)
	} else if ident, ok := se.X.(*ast.Ident); ok {
		log.Printf("Ident '%s' found, looking up type info...\n", ident.Name)
		if obj, ok := info.Uses[ident]; ok {
			log.Printf("Object '%s' found with type '%s'\n", ident.Name, obj.Type())
		} else {
			log.Printf("Object '%s' not found in type info\n", ident.Name)
		}
	}
	log.Printf("Function '%s'\n", se.Sel.Name)
}

func TestInspectNestedExpr(t *testing.T) {
	src := `package main

import (
  "fmt"
)

type Calculator struct {}

func NewCalculator() *Calculator {
  return &Calculator{}
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA(base int) *A {
  return &A{calculator: NewCalculator(), base: base}
}

func (a *A) calc1(v int) int {
  return a.calculator.add(v, a.base)
}

func main() {
  ai := NewA(10)
  ai.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				if x, ok := fun.X.(*ast.SelectorExpr); ok {
					if ident, ok := x.X.(*ast.Ident); ok {
						log.Printf("Recv '%s', Intermediate '%s', Function '%s'\n", ident.Name, x.Sel.Name, fun.Sel.Name) // Recv 'a', Intermediate 'calculator', Function 'add'
					}
				} else if ident, ok := fun.X.(*ast.Ident); ok {
					log.Printf("Recv '%s', Function '%s'\n", ident.Name, fun.Sel.Name) // Recv 'a', Function 'calculator'
				}
			}
		}
		return true
	})
}

func TestInspectNestedExpr1(t *testing.T) {
	src := `package main

type Calculator struct {
  nested *Calculator
}

func NewCalculator() *Calculator {
  return &Calculator{
    nested: &Calculator{
      nested: &Calculator{
        nested: &Calculator{},
      },
    },
  }
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA(base int) *A {
  return &A{calculator: NewCalculator(), base: base}
}

func (a *A) calc1(v int) int {
  return a.calculator.nested.nested.nested.add(v, a.base)
}

func main() {
  ai := NewA(10)
  ai.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				traverseSelectorExpr(fun)
			}
		}
		return true
	})
}

func TestInspectNestedExpr2(t *testing.T) {
	src := `package main

type Calculator struct {
  nested *Calculator
}

func NewCalculator() *Calculator {
  return &Calculator{
    nested: &Calculator{
      nested: &Calculator{
        nested: &Calculator{},
      },
    },
  }
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA(base int) *A {
  return &A{calculator: NewCalculator(), base: base}
}

func (a *A) calc1(v int) int {
  return a.calculator.nested.nested.nested.add(v, a.base)
}

func main() {
  ai := NewA(10)
  ai.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Uses:  make(map[*ast.Ident]types.Object),
	}

	_, err = conf.Check("", fset, []*ast.File{file}, info)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				traverseSelectorExpr2(fun, info, fset)
			}
		}
		return true
	})
}

func TestInspectNestedExpr3(t *testing.T) {
	src := `package main

type Calculator struct {
  nested *Calculator
}

func NewCalculator() *Calculator {
  return &Calculator{
    nested: &Calculator{
      nested: &Calculator{
        nested: &Calculator{},
      },
    },
  }
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA(base int) *A {
  return &A{calculator: NewCalculator(), base: base}
}

func (a *A) calc1(v int) int {
  return a.calculator.nested.nested.nested.add(v, a.base)
}

func main() {
  ai := NewA(10)
  ai.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	_, err = conf.Check("", fset, []*ast.File{file}, info)
	if err != nil {
		log.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				traverseSelectorExpr3(fun, info, fset)
			}
		}
		return true
	})
}

func TestInspectNestedFunctions(t *testing.T) {
	src := `package main

type Calculator struct {
  nested *Calculator
}

func NewCalculator() *Calculator {
  return &Calculator{
    nested: &Calculator{
      nested: &Calculator{
        nested: &Calculator{},
      },
    },
  }
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA(base int) *A {
  return &A{calculator: NewCalculator(), base: base}
}

func (a *A) calc1(v int) int {
  return a.calculator.nested.nested.nested.add(v, a.base)
}

func main() {
  ai := NewA(10)
  ai.calc1(1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		log.Fatal(err)
	}

	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	_, err = conf.Check("", fset, []*ast.File{file}, info)
	if err != nil {
		log.Fatal(err)
	}

	var mainFn *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		if fn, isFunc := n.(*ast.FuncDecl); isFunc && fn.Name.Name == "main" {
			mainFn = fn
			return false // main関数が見つかったので巡回を終了
		}
		return true
	})

	if mainFn != nil {
		ast.Inspect(mainFn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				traverseCallExpr(call, info, fset)
			}
			return true
		})
	}
}

func traverseCallExpr(ce *ast.CallExpr, info *types.Info, fset *token.FileSet) {
	if fun, ok := ce.Fun.(*ast.SelectorExpr); ok {
		traverseSelectorExpr3(fun, info, fset)
	}
	for _, arg := range ce.Args {
		if nestedCall, ok := arg.(*ast.CallExpr); ok {
			traverseCallExpr(nestedCall, info, fset)
		}
	}

	// TODO: calc1 が呼び出している関数も解析する
	// Ident 'ai' found, looking up type info...
	// Object 'ai' found with type '*A'
	// Function 'calc1'
	// map[fn]*ast.FuncDecl
	// if main called calc1, find calc1 called functions...
}

func TestInspectFunctionReferencesSSA(t *testing.T) {
	src := `package main

type Calculator struct {}

func NewCalculator() *Calculator {
  return &Calculator{}
}

func (c *Calculator) add(a, b int) int {
  return a + b
}

func (c *Calculator) sub(a, b int) int {
  return a - b
}

type A struct{
  base int
  calculator *Calculator
}

func NewA() *A {
  return &A{calculator: NewCalculator()}
}

func (a *A) calc1(v int) int {
  return a.calculator.add(v, a.base)
}

func main() {
  ai := NewA()
  ai.calc1(1)
}
`
	// Load the package
	conf := loader.Config{ParserMode: parser.ParseComments}
	f, err := conf.ParseFile("main.go", src)
	if err != nil {
		t.Fatal(err)
	}
	conf.CreateFromFiles("main", f)
	prog, err := conf.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Create SSA representation
	ssaProg := ssautil.CreateProgram(prog, ssa.SanityCheckFunctions)
	ssaProg.Build()

	// Inspect SSA functions
	for _, pkg := range ssaProg.AllPackages() {
		for _, mem := range pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				fmt.Printf("Function: %s\n", fn.Name())
				for _, block := range fn.Blocks {
					for _, instr := range block.Instrs {
						if call, ok := instr.(*ssa.Call); ok {
							callee := call.Call.StaticCallee()
							if callee != nil && callee.Name() == "add" {
								fmt.Printf("  %s calls add\n", fn.Name())
							}
						}
					}
				}
			}
		}
	}
}

func TestReplaceFmtSSA(t *testing.T) {
	src := `package main

import (
	"fmt"
)

func main() {
	a := 1
	fmt.Println(a)

	b := "hello"
	fmt.Println(b)
}
`
	conf := loader.Config{
		ParserMode: parser.ParseComments,
		Build:      &build.Default,
	}
	f, err := conf.ParseFile("main.go", src)
	if err != nil {
		t.Fatal(err)
	}
	conf.CreateFromFiles("main", f)
	prog, err := conf.Load()
	if err != nil {
		t.Fatal(err)
	}

	ssaProg := ssautil.CreateProgram(prog, ssa.SanityCheckFunctions)
	ssaProg.Build()

	for _, pkg := range ssaProg.AllPackages() {
		for _, mem := range pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				for _, block := range fn.Blocks {
					for i, instr := range block.Instrs {
						if call, ok := instr.(*ssa.Call); ok {
							if callee := call.Call.StaticCallee(); callee != nil && callee.Name() == "Println" && callee.Pkg.Pkg.Name() == "fmt" {
								// TODO: type is []any
								fmt.Println(call.Call.Args[0].Type())
								if len(call.Call.Args) == 1 {
									arg := call.Call.Args[0]
									var newFormat constant.Value
									newFormat = constant.MakeString("%d")
									printfFn := pkg.Func("fmt.Printf")
									newCall := &ssa.Call{
										Call: ssa.CallCommon{
											Value: printfFn,
											Args:  []ssa.Value{ssa.NewConst(newFormat, types.Typ[types.String]), arg},
										},
									}

									block.Instrs[i] = newCall
								}
							}
						}
					}
				}
			}
		}
	}
}

// buildutil.FakeContext wrapper
func fakeContext(pkgs map[string]string) *build.Context {
	npkgs := make(map[string]map[string]string)
	for path, content := range pkgs {
		npkgs[path] = map[string]string{"x.go": content}
	}
	return buildutil.FakeContext(npkgs)
}

func printGraph(cg *callgraph.Graph, from *types.Package, edgeMatch string, desc string) string {
	var edges []string
	callgraph.GraphVisitEdges(cg, func(e *callgraph.Edge) error {
		if strings.Contains(e.Description(), edgeMatch) {
			edges = append(edges, fmt.Sprintf("%s --> %s",
				e.Caller.Func.RelString(from),
				e.Callee.Func.RelString(from)))
		}
		return nil
	})
	sort.Strings(edges)

	var buf bytes.Buffer
	buf.WriteString(desc + "\n")
	for _, edge := range edges {
		fmt.Fprintf(&buf, "  %s\n", edge)
	}
	return strings.TrimSpace(buf.String())
}

func TestSSACallGraph(t *testing.T) {
	main := `
package main

import (
	"example"
)

type MyStructA struct {
	example.MyStructB
}

type MyStructC struct {
	field int
}

func (ms MyStructA) Method1() int {
	return ms.MyStructB.Field1
}

func main() {
	a := &MyStructA{}
	a.Method1()
}
`

	example := `package example

type MyStructB struct {
  Field1 int
}
	`

	conf := loader.Config{
		ParserMode: parser.ParseComments,
		Build:      fakeContext(map[string]string{"main": main, "example": example}),
	}
	conf.Import("main")
	iprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	prog := ssautil.CreateProgram(iprog, ssa.InstantiateGenerics)
	prog.Build()

	fmt.Println(prog.AllPackages())
	fmt.Println(ssautil.AllFunctions(prog))
	cg := cha.CallGraph(prog)
	cg.DeleteSyntheticNodes()
	fmt.Println(printGraph(cg, nil, "", "All calls"))
}

// TODO: test reaching definition without ssa
// https://en.wikipedia.org/wiki/Reaching_definition

// helper functions
func getParentNode(node ast.Node) ast.Node {
	var parent ast.Node
	ast.Inspect(node, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.File, *ast.BlockStmt:
			parent = n
		}
		return parent == nil
	})
	return parent
}

func replaceStmtInPlace(file *ast.File, old, new ast.Stmt) {
	for _, stmt := range file.Decls {
		if decl, ok := stmt.(*ast.FuncDecl); ok {
			for j, bodyStmt := range decl.Body.List {
				if bodyStmt == old {
					decl.Body.List[j] = new
					return
				}
			}
		}
	}
}

func formatFunctionDefinition(funcDecl *ast.FuncDecl) string {
	var buf bytes.Buffer
	err := format.Node(&buf, token.NewFileSet(), funcDecl)
	if err != nil {
		log.Fatalf("Failed to format function definition: %v", err)
	}
	return buf.String()
}
