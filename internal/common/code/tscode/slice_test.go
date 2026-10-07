package tscode

import (
	"strings"
	"testing"
)

func TestSliceScriptBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source, symbol, want string
	}{
		{"function", "import { x } from 'x';\n/** doc */\nexport async function load<T extends {id: number}>(\n input: T = {id: 1} as T,\n): Promise<{value: T}> {\n const nested = () => ({a: '}'});\n return {value: input};\n}\nfunction unrelated() {}", "load", "/** doc */\nexport async function load<T extends {id: number}>(\n input: T = {id: 1} as T,\n): Promise<{value: T}> {\n const nested = () => ({a: '}'});\n return {value: input};\n}"},
		{"object return", "function first(): {x: number} | {y: string} { return {x: 1}; }\nfunction second() {}", "first", "function first(): {x: number} | {y: string} { return {x: 1}; }"},
		{"arrow expression", "const first = (n: number) =>\n  n + 1\nconst second = () => 2\n", "first", "const first = (n: number) =>\n  n + 1"},
		{"computed callback", "const total = computed(() => { return {n: 1}; });\nconst next = 2;", "total", "const total = computed(() => { return {n: 1}; });"},
		{"regex and strings", "function first() { const a = /[}\\/]/g; if (a) /}/.test('}'); return `outer ${`${{x: '}'}.x}`} end`; }\nfunction second() {}", "first", "function first() { const a = /[}\\/]/g; if (a) /}/.test('}'); return `outer ${`${{x: '}'}.x}`} end`; }"},
		{"division", "function first(n) { return n / 2 / 3; } function next() {}", "first", "function first(n) { return n / 2 / 3; }"},
		{"method", "class A { before() {}\n/** Method doc. */\n@memo({key: '}'})\npublic async target<T>(arg: T): Promise<{value: T}> { return {value: arg}; }\nafter() {} }", "A.target", "/** Method doc. */\n@memo({key: '}'})\npublic async target<T>(arg: T): Promise<{value: T}> { return {value: arg}; }"},
		{"arrow field", "class A {\n readonly target = (n: number) => ({n})\n next() {}\n}", "target", " readonly target = (n: number) => ({n})"},
		{"private generator", "class A { *#values() { yield 1; } next() {} }", "A.#values", "*#values() { yield 1; }"},
		{"whole class", "export class A { first() {} }\nclass B {}", "A", "export class A { first() {} }"},
		{"object function", "const API = { first: async function (n = {}) { return n; }, second() {} };", "API.first", "first: async function (n = {}) { return n; }"},
		{"object arrow", "const API = { first: async (): Promise<void> => {}, second() {} };", "API.first", "first: async (): Promise<void> => {}"},
		{"object method", "const API = { first<T>(n: T): T { return n; }, second() {} };", "API.first", "first<T>(n: T): T { return n; }"},
		{"object generic method", "const API = { first<T, U>(n: T, m: U) { return [n, m]; }, second() {} };", "API.first", "first<T, U>(n: T, m: U) { return [n, m]; }"},
		{"object generic return", "const API = { first(): Map<string, number> { return new Map(); }, second() {} };", "API.first", "first(): Map<string, number> { return new Map(); }"},
		{"object function return type", "const API = { first: function(): Map<string, number> { return new Map(); }, second() {} };", "API.first", "first: function(): Map<string, number> { return new Map(); }"},
		{"object arrow return type", "const API = { first: (): Map<string, number> => new Map<string, number>(), second() {} };", "API.first", "first: (): Map<string, number> => new Map<string, number>()"},
		{"object arrow generic call", "const API = { first: () => call<string, number>(a, b), second() {} };", "API.first", "first: () => call<string, number>(a, b)"},
		{"object generic arrow", "const API = { first: <T, U>(a: T, b: U) => ({a, b}), second() {} };", "API.first", "first: <T, U>(a: T, b: U) => ({a, b})"},
		{"top level wins", "function first() {}\nconst API = { first() { return 2; } };", "first", "function first() {}"},
		{"generic default", "function first<T = {x: number}>(n: T): T { return n; }\nfunction second() {}", "first", "function first<T = {x: number}>(n: T): T { return n; }"},
		{"readonly structural return", "function first(): readonly {x: number}[] { return []; }\nfunction second() {}", "first", "function first(): readonly {x: number}[] { return []; }"},
		{"abstract ASI", "abstract class A {\n abstract first(): void\n second() { return 2; }\n}", "A.first", " abstract first(): void"},
		{"type", "type Value = {\n x: number;\n} | string;\nconst next = 1;", "Value", "type Value = {\n x: number;\n} | string;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Slice([]byte(tc.source), tc.symbol)
			if err != nil || got != tc.want {
				t.Fatalf("Slice(%s): %v\ngot: %s\nwant: %s", tc.symbol, err, got, tc.want)
			}
		})
	}
}

func TestSliceAmbiguityAndNestedScopes(t *testing.T) {
	src := []byte("class A { target() { function nested() {} } }\nclass B { target() {} }\nfunction outer() { const local = 2; }")
	if _, names, err := Slice(src, "target"); err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(strings.Join(names, ","), "B.target") {
		t.Fatalf("ambiguous method: %v, %v", err, names)
	}
	if got, _, err := Slice(src, "B.target"); err != nil || got != "target() {}" {
		t.Fatalf("qualified method: %q, %v", got, err)
	}
	for _, name := range []string{"nested", "local", "missing"} {
		if _, _, err := Slice(src, name); err == nil {
			t.Fatalf("unexpected top-level symbol %s", name)
		}
	}
}

func TestSliceAccessorsAndOverloads(t *testing.T) {
	src := []byte("class A { get value() { return 1; } set value(n) { this.n = n; } next() {} }")
	got, _, err := Slice(src, "A.value")
	if err != nil || got != "get value() { return 1; }\nset value(n) { this.n = n; }" {
		t.Fatalf("accessors: %q, %v", got, err)
	}
	src = []byte("function load(n: number): number;\nfunction load(n: string): string;\nfunction load(n: unknown) { return n; }\nfunction next() {}")
	got, _, err = Slice(src, "load")
	if err != nil || !strings.Contains(got, "load(n: number)") || !strings.Contains(got, "load(n: string)") || !strings.Contains(got, "return n;") || strings.Contains(got, "next") {
		t.Fatalf("overloads: %q, %v", got, err)
	}
}

func TestSliceVueBothScriptsAndNoTemplateLeak(t *testing.T) {
	src := []byte("<!-- <script>function fake() {}</script> -->\n<template><button @click=\"save\">{}</button></template>\n<script lang=\"ts\">export function helper() { return 1; }</script>\n<script setup lang=\"ts\">\nconst save = async () => { await helper(); };\nconst other = 2;\n</script>\n<style>.save { color: red; }</style>")
	for _, tc := range []struct{ name, want string }{
		{"helper", "export function helper() { return 1; }"},
		{"save", "const save = async () => { await helper(); };"},
	} {
		got, _, err := SliceVue(src, tc.name)
		if err != nil || got != tc.want {
			t.Fatalf("Vue %s: %q, %v", tc.name, got, err)
		}
	}
	if _, _, err := SliceVue(src, "fake"); err == nil {
		t.Fatal("HTML comment leaked into script symbols")
	}
	if _, _, err := SliceVue([]byte("<script>const x = 1;</script><script setup>const x = 2;</script>"), "x"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate script names: %v", err)
	}
}

func TestSliceRejectsIncompleteAndUnsupportedSyntax(t *testing.T) {
	for _, src := range []string{
		"function x() {", "function x() { /* unfinished", "function x() { return 'unfinished; }",
		"function x() { return `unfinished; }", "function x() { return /[}/; }", "const x = () => <div/>;",
	} {
		if got, _, err := Slice([]byte(src), "x"); err == nil || got != "" {
			t.Fatalf("accepted incomplete/unsupported source: %q -> %q, %v", src, got, err)
		}
	}
	if _, _, err := SliceVue([]byte("<script src='./external.ts'></script>"), "x"); err == nil {
		t.Fatal("external script accepted")
	}
}
