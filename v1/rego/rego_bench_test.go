package rego

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/open-policy-agent/opa/internal/runtime"
	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/loader"
	inmem "github.com/open-policy-agent/opa/v1/storage/inmem/test"
	"github.com/open-policy-agent/opa/v1/util/test"
)

func BenchmarkPartialObjectRuleCrossModule(b *testing.B) {
	ctx := context.Background()
	sizes := []int{10, 100, 1000}

	for _, n := range sizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			store := inmem.NewFromObject(map[string]interface{}{})
			mods := test.PartialObjectBenchmarkCrossModule(n)
			query := "data.test.foo"

			input := make(map[string]interface{})
			for idx := 0; idx <= 3; idx++ {
				input[fmt.Sprintf("test_input_%d", idx)] = "test_input_10"
			}
			inputAST, err := ast.InterfaceToValue(input)
			if err != nil {
				b.Fatal(err)
			}

			compiler := ast.MustCompileModules(map[string]string{
				"test/foo.rego": mods[0],
				"test/bar.rego": mods[1],
				"test/baz.rego": mods[2],
			})
			info, err := runtime.Term(runtime.Params{})
			if err != nil {
				b.Fatal(err)
			}

			pq, err := New(
				Query(query),
				Compiler(compiler),
				Store(store),
				Runtime(info),
			).PrepareForEval(ctx)

			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err = pq.Eval(
					ctx,
					EvalParsedInput(inputAST),
					EvalRuleIndexing(true),
					EvalEarlyExit(true),
				)

				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCustomFunctionInHotPath(b *testing.B) {
	ctx := context.Background()
	input := ast.MustParseTerm(mustReadFileAsString(b, "testdata/ast.json"))
	module := ast.MustParseModule(`package test

	import rego.v1

	r := count(refs)

	refs contains value if {
		walk(input, [_, value])
		is_ref(value)
	}

	is_ref(value) if value.type == "ref"
	is_ref(value) if value[0].type == "ref"`)

	r := New(Query("data.test.r = x"), ParsedModule(module))

	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := pq.Eval(ctx, EvalParsedInput(input.Value))
		if err != nil {
			b.Fatal(err)
		}

		if res == nil {
			b.Fatal("expected result")
		}

		if res[0].Bindings["x"].(json.Number) != "402" {
			b.Fatalf("expected 402, got %v", res[0].Bindings["x"])
		}
	}
}

// Benchmarks of the ACI test data from Regorus
// https://github.com/microsoft/regorus?tab=readme-ov-file#performance

// BenchmarkAciTestBuildAndEval-10    37    30700209 ns/op    16437935 B/op    384211 allocs/op
func BenchmarkAciTestBuildAndEval(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		bundle, err := loader.NewFileLoader().
			WithRegoVersion(ast.RegoV0).
			AsBundle("testdata/aci")
		if err != nil {
			b.Fatal(err)
		}

		input := ast.MustParseTerm(mustReadFileAsString(b, "testdata/aci/input.json"))

		r := New(Query("data.framework.mount_overlay = x"), ParsedBundle("", bundle))

		pq, err := r.PrepareForEval(ctx)
		if err != nil {
			b.Fatal(err)
		}

		res, err := pq.Eval(ctx, EvalParsedInput(input.Value))
		if err != nil {
			b.Fatal(err)
		}

		_ = res
	}
}

// BenchmarkAciTestOnlyEval-10    12752    92188 ns/op    50005 B/op    1062 allocs/op
func BenchmarkAciTestOnlyEval(b *testing.B) {
	ctx := context.Background()

	bundle, err := loader.NewFileLoader().
		WithRegoVersion(ast.RegoV0).
		AsBundle("testdata/aci")
	if err != nil {
		b.Fatal(err)
	}

	input := ast.MustParseTerm(mustReadFileAsString(b, "testdata/aci/input.json"))

	r := New(Query("data.framework.mount_overlay = x"), ParsedBundle("", bundle))

	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := pq.Eval(ctx, EvalParsedInput(input.Value))
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

// BenchmarkArrayIteration-10
// 15574    77121 ns/op    67249 B/op    1115 allocs/op    // handleErr wrapping, not inlined
// 33862    35864 ns/op     5768 B/op      93 allocs/op    // handleErr only on error, inlined
func BenchmarkArrayIteration(b *testing.B) {
	ctx := context.Background()

	at := make([]*ast.Term, 512)
	for i := 0; i < 511; i++ {
		at[i] = ast.StringTerm("a")
	}
	at[511] = ast.StringTerm("v")

	input := ast.NewObject(ast.Item(ast.StringTerm("foo"), ast.ArrayTerm(at...)))
	module := ast.MustParseModule(`package test

	default r := false

	r if input.foo[_] == "v"`)

	r := New(Query("data.test.r = x"), ParsedModule(module))

	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := pq.Eval(ctx, EvalParsedInput(input))
		if err != nil {
			b.Fatal(err)
		}

		if res == nil {
			b.Fatal("expected result")
		}

		if res[0].Bindings["x"].(bool) != true {
			b.Fatalf("expected true, got %v", res[0].Bindings["x"])
		}
	}
}

// BenchmarkSetIteration-10
// 4800    272403 ns/op    80875 B/op    1193 allocs/op    // handleErr wrapping, not inlined
// 4933	   223234 ns/op	   76772 B/op	  681 allocs/op    // handleErr only on error, not inlined
func BenchmarkSetIteration(b *testing.B) {
	ctx := context.Background()

	at := make([]*ast.Term, 512)
	for i := 0; i < 512; i++ {
		at[i] = ast.StringTerm(strconv.Itoa(i))
	}

	input := ast.NewObject(ast.Item(ast.StringTerm("foo"), ast.ArrayTerm(at...)))
	module := ast.MustParseModule(`package test

	s := {x | x := input.foo[_]}

	default r := false

	r if s[_] == "not found"`)

	r := New(Query("data.test.r = x"), ParsedModule(module))

	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := pq.Eval(ctx, EvalParsedInput(input))
		if err != nil {
			b.Fatal(err)
		}
		if res == nil {
			b.Fatal("expected result")
		}
		if res[0].Bindings["x"].(bool) != false {
			b.Fatalf("expected false, got %v", res[0].Bindings["x"])
		}
	}
}

// BenchmarkObjectIteration-10
// 12067    99582 ns/op    72830 B/op   1126 allocs/op    // handleErr wrapping, not inlined
// 15358    85080 ns/op    27752 B/op    615 allocs/op    // handleErr only on error, not inlined
func BenchmarkObjectIteration(b *testing.B) {
	ctx := context.Background()

	at := make([][2]*ast.Term, 512)
	for i := 0; i < 512; i++ {
		at[i] = ast.Item(ast.StringTerm(strconv.Itoa(i)), ast.StringTerm(strconv.Itoa(i)))
	}

	input := ast.NewObject(ast.Item(ast.StringTerm("foo"), ast.ObjectTerm(at...)))
	module := ast.MustParseModule(`package test

	default r := false

	r if {
		input.foo[_] == "512"
	}
		`)

	r := New(Query("data.test.r = x"), ParsedModule(module))

	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := pq.Eval(ctx, EvalParsedInput(input))
		if err != nil {
			b.Fatal(err)
		}
		if res == nil {
			b.Fatal("expected result")
		}
		if res[0].Bindings["x"].(bool) != false {
			b.Fatalf("expected false, got %v", res[0].Bindings["x"])
		}
	}
}

func mustReadFileAsString(b *testing.B, path string) string {
	b.Helper()

	bs, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	return string(bs)
}
