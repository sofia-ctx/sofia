package vue

import "testing"

const sampleRouter = `import { createRouter, createWebHistory } from 'vue-router'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('../views/LoginView.vue'),
      meta: { public: true },
    },
    {
      path: '/',
      component: () => import('../layouts/AppLayout.vue'),
      children: [
        {
          path: '',
          name: 'dashboard',
          component: () => import('../views/DashboardView.vue'),
        },
        {
          path: 'tasks',
          name: 'my-tasks',
          component: () => import('../views/MyTasksView.vue'),
          meta: { requiresRole: 'agent' },
        },
      ],
    },
  ],
})
`

func TestParseRoutes(t *testing.T) {
	routes := Parse(sampleRouter)
	if len(routes) != 4 {
		t.Fatalf("got %d routes, want 4: %+v", len(routes), routes)
	}

	// Two routes share path "/" (the layout wrapper and its index child), so
	// assert by iterating, not by a path-keyed map.
	loginFound, layoutFound, dashboardFound, tasksFound := false, false, false, false
	for _, r := range routes {
		switch {
		case r.Path == "/login" && r.Name == "login" && r.Component == "LoginView" && r.Meta == "public: true":
			loginFound = true
		case r.Path == "/" && r.Component == "AppLayout" && r.Name == "":
			layoutFound = true
		case r.Path == "/" && r.Name == "dashboard" && r.Component == "DashboardView":
			dashboardFound = true
		case r.Path == "/tasks" && r.Name == "my-tasks" && r.Component == "MyTasksView" && r.Meta == "requiresRole: 'agent'":
			tasksFound = true
		}
	}
	if !loginFound {
		t.Errorf("/login route wrong: %+v", routes)
	}
	if !layoutFound {
		t.Errorf("layout wrapper '/' → AppLayout missing: %+v", routes)
	}
	if !dashboardFound {
		t.Errorf("index child should resolve to '/': %+v", routes)
	}
	if !tasksFound {
		t.Errorf("nested child path should join to '/tasks': %+v", routes)
	}
}

func TestJoinPath(t *testing.T) {
	cases := []struct{ parent, child, want string }{
		{"", "/login", "/login"},
		{"/", "", "/"},
		{"/", "tasks", "/tasks"},
		{"/admin", "users", "/admin/users"},
		{"/admin", "/abs", "/abs"},
		{"", "", "/"},
	}
	for _, c := range cases {
		if got := joinPath(c.parent, c.child); got != c.want {
			t.Errorf("joinPath(%q,%q) = %q, want %q", c.parent, c.child, got, c.want)
		}
	}
}

func TestParseNoRoutes(t *testing.T) {
	if r := Parse("export const x = 1\n"); r != nil {
		t.Errorf("no routes array → nil, got %+v", r)
	}
}
