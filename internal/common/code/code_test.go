package code

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePHP = `<?php
declare(strict_types=1);
namespace App\User\Features\ApproveUser\EntryPoint\Http;

use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\Security\Http\Attribute\IsGranted;

#[IsGranted('ROLE_OWNER')]
final class ApproveUserController
{
    public function __construct(private MessageBusInterface $bus) {}

    #[Route('/api/v1/users/{id}/approve', name: 'api_v1_users_approve', methods: ['POST'])]
    public function __invoke(string $id): JsonResponse
    {
        return new JsonResponse();
    }
}
`

func TestRunPHP(t *testing.T) {
	structuralOnly(t)
	p := filepath.Join(t.TempDir(), "ApproveUserController.php")
	if err := os.WriteFile(p, []byte(samplePHP), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run php: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"kind: class",
		"name: ApproveUserController",
		"modifiers: final",
		"IsGranted(ROLE_OWNER)",            // class attribute
		"ctor[1]",                          // constructor captured
		"MessageBusInterface",              // ctor dep type
		",true",                            // promoted ctor property
		"__invoke",                         // method
		"Route(/api/v1/users/{id}/approve", // method attribute with args
		"methods: [POST]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("php output missing %q in:\n%s", want, out)
		}
	}
}

func TestRunMultiFile(t *testing.T) {
	structuralOnly(t)
	dir := t.TempDir()
	goP := filepath.Join(dir, "a.go")
	phpP := filepath.Join(dir, "b.php")
	if err := os.WriteFile(goP, []byte("package a\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(phpP, []byte(samplePHP), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{goP, phpP}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run multi: %v", err)
	}
	out := buf.String()
	// Both files present in one aggregated output (the tiny Go file is below the
	// compact-or-raw threshold, so it comes back as raw — that's expected).
	if !strings.Contains(out, "Hello") {
		t.Errorf("missing Go block:\n%s", out)
	}
	if !strings.Contains(out, "kind: class") || !strings.Contains(out, "ApproveUserController") {
		t.Errorf("missing PHP block:\n%s", out)
	}
	// ...in input order (Go before PHP).
	if strings.Index(out, "Hello") > strings.Index(out, "ApproveUserController") {
		t.Errorf("blocks out of order:\n%s", out)
	}
}
