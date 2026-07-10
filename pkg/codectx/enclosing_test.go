package codectx

import "testing"

func TestEnclosing_PHP(t *testing.T) {
	lines := []string{
		`<?php`,
		`class UserService {`,
		`  public function deleteUser() {`,
		`    if ($x) {`,
		`      return;`,
		`    }`,
		`  }`,
		`}`,
	}
	if got := Enclosing(lines, 3, ".php"); got != "function deleteUser" {
		t.Errorf("got %q, want 'function deleteUser'", got)
	}
	if got := Enclosing(lines, 1, ".php"); got != "" {
		t.Errorf("expected no enclosing above class decl, got %q", got)
	}
}

func TestEnclosing_TS(t *testing.T) {
	lines := []string{
		`export class Foo {`,
		`  bar() {`,
		`    console.log('x');`,
		`  }`,
		`}`,
	}
	got := Enclosing(lines, 2, ".ts")
	if got != "class Foo" {
		t.Errorf("got %q, want 'class Foo'", got)
	}
}

func TestEnclosing_Twig(t *testing.T) {
	lines := []string{
		`{% block content %}`,
		`  <p>hello</p>`,
		`{% endblock %}`,
	}
	if got := Enclosing(lines, 1, ".twig"); got != "block content" {
		t.Errorf("got %q", got)
	}
}

func TestEnclosing_INI(t *testing.T) {
	lines := []string{
		`[section]`,
		`key = value`,
	}
	if got := Enclosing(lines, 1, ".ini"); got != "[section]" {
		t.Errorf("got %q", got)
	}
}

func TestEnclosing_UnknownExt(t *testing.T) {
	lines := []string{`anything`, `here`}
	if got := Enclosing(lines, 1, ".unknown"); got != "" {
		t.Errorf("expected empty for unsupported ext, got %q", got)
	}
}

func TestEnclosing_NoMatch(t *testing.T) {
	lines := []string{`just text`, `still text`}
	if got := Enclosing(lines, 1, ".php"); got != "" {
		t.Errorf("expected empty when no scope, got %q", got)
	}
}
