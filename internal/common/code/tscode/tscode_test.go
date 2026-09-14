package tscode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleVue = `<template>
  <div>
    <TaskDialog :id="taskId" />
    <Button label="Save" @click="load" />
  </div>
</template>
<script setup lang="ts">
import { ref, computed } from 'vue'
import client from '../api/client'
import { type TaskItem } from '../api/types'
import { useAuthStore } from '../stores/auth'

const props = defineProps<{ taskId: string; visible: boolean }>()
const emit = defineEmits<{ 'update:visible': [boolean]; logged: [] }>()
const open = defineModel<boolean>('open')

const auth = useAuthStore()
const loading = ref(false)
async function load(): Promise<void> { await client.getTask(props.taskId) }
</script>`

const sampleTS = `import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface CurrentUser {
  id: string
  name: string
  roles: string[]
}

export enum DealStatus { Open, Won, Lost }

export type Id = string

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(null)
  return { token }
})

function helper(): void {}
`

func readTS(t *testing.T, name, body string) *TSFile {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ReadTS(p)
	if err != nil {
		t.Fatalf("ReadTS: %v", err)
	}
	return f
}

func TestReadVue(t *testing.T) {
	f := readTS(t, "MyTasksView.vue", sampleVue)
	if f.Lang != "vue" || f.Component != "MyTasksView" {
		t.Errorf("lang/component = %s/%s", f.Lang, f.Component)
	}
	if !has(f.Imports, "vue") || !has(f.Imports, "../api/client") {
		t.Errorf("imports = %v", f.Imports)
	}
	if !has(f.Props, "taskId") || !has(f.Props, "visible") {
		t.Errorf("props = %v, want taskId,visible", f.Props)
	}
	if !has(f.Emits, "update:visible") || !has(f.Emits, "logged") {
		t.Errorf("emits = %v", f.Emits)
	}
	if !has(f.Models, "open") {
		t.Errorf("models = %v, want open", f.Models)
	}
	if !has(f.Stores, "useAuthStore") {
		t.Errorf("stores = %v, want useAuthStore", f.Stores)
	}
	if !has(f.APICalls, "getTask") {
		t.Errorf("api_calls = %v, want getTask", f.APICalls)
	}
	if !has(f.Components, "TaskDialog") || !has(f.Components, "Button") {
		t.Errorf("components = %v, want TaskDialog,Button", f.Components)
	}
	if !hasSym(f, "function", "load") || !hasSym(f, "const", "loading") {
		t.Errorf("symbols = %+v", f.Symbols)
	}
}

func TestReadTS(t *testing.T) {
	f := readTS(t, "auth.ts", sampleTS)
	if f.Lang != "ts" {
		t.Errorf("lang = %s", f.Lang)
	}
	ci := findType(f, "CurrentUser")
	if ci == nil || ci.Kind != "interface" || !ci.Exported {
		t.Fatalf("missing exported interface CurrentUser: %+v", f.Types)
	}
	if ci.Members != "id: string; name: string; roles: string[]" {
		t.Errorf("CurrentUser members = %q", ci.Members)
	}
	if e := findType(f, "DealStatus"); e == nil || e.Kind != "enum" || e.Members != "Open, Won, Lost" {
		t.Errorf("enum DealStatus = %+v", e)
	}
	if a := findType(f, "Id"); a == nil || a.Kind != "type" || a.Members != "string" {
		t.Errorf("type alias Id = %+v", a)
	}
	if !hasSymExp(f, "const", "useAuthStore", true) {
		t.Errorf("missing exported const useAuthStore: %+v", f.Symbols)
	}
	if !hasSymExp(f, "function", "helper", false) {
		t.Errorf("missing non-exported function helper: %+v", f.Symbols)
	}
}

func findType(f *TSFile, name string) *TSType {
	for i := range f.Types {
		if f.Types[i].Name == name {
			return &f.Types[i]
		}
	}
	return nil
}

func has(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func hasSym(f *TSFile, kind, name string) bool {
	for _, s := range f.Symbols {
		if s.Kind == kind && s.Name == name {
			return true
		}
	}
	return false
}

func hasSymExp(f *TSFile, kind, name string, exported bool) bool {
	for _, s := range f.Symbols {
		if s.Kind == kind && s.Name == name && s.Exported == exported {
			return true
		}
	}
	return false
}

const sampleMJS = `import { readFile } from "node:fs/promises";
import path from "node:path";

export const VERSION = "1.2";

const cache = new Map();

export async function sweep(root) {
  return walk(root);
}

async function walk(dir) {
  return dir;
}

export class Reporter {
  print() {}
}

export default sweep;
`

func TestReadJS(t *testing.T) {
	f := readTS(t, "sweep.mjs", sampleMJS)
	if f.Lang != "js" {
		t.Fatalf("lang = %q, want js", f.Lang)
	}
	if got := strings.Join(f.Imports, ","); got != "node:fs/promises,node:path" {
		t.Fatalf("imports = %q", got)
	}
	var names []string
	for _, s := range f.Symbols {
		names = append(names, s.Kind+":"+s.Name)
	}
	want := "const:VERSION,const:cache,function:sweep,function:walk"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("symbols = %q, want %q", got, want)
	}
	c := findType(f, "Reporter")
	if c == nil || c.Kind != "class" || !c.Exported || c.Members != "print" {
		t.Fatalf("class Reporter = %+v", c)
	}
}

const sampleClassJS = `class FakeAudioContext {
    constructor(opts = {}) { this.state = 'running'; }
    get currentTime() { return 0; }
    resume() {} suspend() {}
    createBuffer(ch, len) { return { getChannelData: () => new Float32Array(len), length: len }; }
    createGain() { return audioNode; } createOscillator() { return audioNode; }
    async strikeClip(atk, def) {
        if (atk) { this.hit(atk); }
        for (const c of def) { helper(c); }
        function inner() {}
    }
    #secret() {}
    static of(x) { return x; }
    *items() {}
    draw = (ctx) => { ctx.fill(); }
}
`

func TestClassMethodsJS(t *testing.T) {
	f := readTS(t, "audio.mjs", sampleClassJS)
	c := findType(f, "FakeAudioContext")
	if c == nil || c.Kind != "class" || c.Exported {
		t.Fatalf("class = %+v", c)
	}
	want := "constructor, currentTime, resume, suspend, createBuffer, createGain, createOscillator, strikeClip, #secret, of, items, draw"
	if c.Members != want {
		t.Fatalf("members =\n  %q\nwant\n  %q", c.Members, want)
	}
	if len(f.Symbols) != 0 {
		t.Fatalf("nothing inside the class body may leak into symbols: %+v", f.Symbols)
	}
}

const sampleClassTS = `export abstract class Service<T> implements Runnable {
  private cache = new Map<string, T>();
  constructor(private readonly repo: Repo<T>) {}
  public async run(id: string): Promise<T | null> {
    return this.repo.find(id);
  }
  protected abstract validate(v: T): boolean;
  handle = async (e: Event): Promise<void> => { await this.run(e.id); };
  static create<T>(repo: Repo<T>): Service<T> { return new Impl(repo); }
}
`

func TestClassMethodsTS(t *testing.T) {
	f := readTS(t, "svc.ts", sampleClassTS)
	c := findType(f, "Service")
	if c == nil || c.Kind != "class" || !c.Exported {
		t.Fatalf("class = %+v", c)
	}
	// `validate` is abstract (no body) and so not a method head here.
	if want := "constructor, run, handle, create"; c.Members != want {
		t.Fatalf("members = %q, want %q", c.Members, want)
	}
}

func TestLangOf(t *testing.T) {
	cases := map[string]string{
		"a.ts": "ts", "a.tsx": "ts", "a.mts": "ts", "a.cts": "ts",
		"a.js": "js", "a.jsx": "js", "a.mjs": "js", "a.cjs": "js",
		"A.vue": "vue",
	}
	for in, want := range cases {
		if got := langOf(in); got != want {
			t.Errorf("langOf(%q) = %q, want %q", in, got, want)
		}
	}
}
