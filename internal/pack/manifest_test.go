package pack

import "testing"

func TestParseManifest_Valid(t *testing.T) {
	data := []byte(`schema: 1
name: xcraft
description: CRM agent pack
plugins:
  - path: plugins/crm
  - git: git@github.com:o/r.git
    ref: v2
instructions:
  - src: instructions/AGENTS.md
    dest: AGENTS.md
claude:
  skills: [ { src: skills/my-skill } ]
  commands: [ { src: commands/deploy.md } ]
templates:
  - src: templates
    dest: .templates
`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Name != "xcraft" || m.Schema != 1 {
		t.Errorf("m = %+v", m)
	}
	if len(m.Plugins) != 2 || m.Plugins[0].Path == "" || m.Plugins[1].Git == "" {
		t.Errorf("plugins = %+v", m.Plugins)
	}
	if len(m.Claude.Skills) != 1 || len(m.Claude.Commands) != 1 {
		t.Errorf("claude = %+v", m.Claude)
	}
}

func TestParseManifest_Rejects(t *testing.T) {
	cases := map[string]string{
		"no name": `schema: 1
`,
		"unsupported schema": `schema: 2
name: xcraft
`,
		"plugin ref with both path and git": `schema: 1
name: xcraft
plugins:
  - path: plugins/crm
    git: git@github.com:o/r.git
`,
		"plugin ref with neither path nor git": `schema: 1
name: xcraft
plugins:
  - ref: v2
`,
		"ref without git": `schema: 1
name: xcraft
plugins:
  - path: plugins/crm
    ref: v2
`,
		"absolute src": `schema: 1
name: xcraft
instructions:
  - src: /etc/passwd
`,
		"dest escapes with ..": `schema: 1
name: xcraft
instructions:
  - src: instructions/AGENTS.md
    dest: ../../etc/passwd
`,
		"invalid name": `schema: 1
name: Xcraft
`,
	}
	for label, doc := range cases {
		t.Run(label, func(t *testing.T) {
			m, err := ParseManifest([]byte(doc))
			if err != nil {
				// A syntax error is an acceptable rejection too.
				return
			}
			if err := m.Validate(); err == nil {
				t.Fatalf("Validate did not reject: %s", doc)
			}
		})
	}
}

func TestSafeRel(t *testing.T) {
	ok := []string{"AGENTS.md", "instructions/AGENTS.md", "a/b/c.md", "."}
	for _, p := range ok {
		if err := safeRel(p); err != nil {
			t.Errorf("safeRel(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{"", "/etc/passwd", "..", "../x", "a/../../b", "a/../.."}
	for _, p := range bad {
		if err := safeRel(p); err == nil {
			t.Errorf("safeRel(%q) = nil, want error", p)
		}
	}
}
