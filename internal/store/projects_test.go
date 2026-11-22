package store

import (
	"reflect"
	"testing"
	"time"
)

func TestProjectsAndTodos(t *testing.T) {
	st := open(t)
	p := Project{ID: "p1", Name: "Diko", Description: "shop", NotesDir: "/notes", DefaultAgent: "cursor", Repos: []string{"/b", "/a"}}
	if err := st.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveProject(Project{ID: "p2", Name: "diko"}); err == nil {
		t.Fatal("project names must be unique regardless of case")
	}
	got, ok, _ := st.Project("DIKO")
	if !ok || got.ID != "p1" || got.MaxParallel != 2 || !reflect.DeepEqual(got.Repos, []string{"/b", "/a"}) {
		t.Fatalf("project = %+v", got)
	}
	got.Repos = []string{"/c"}
	st.SaveProject(got)
	got, _, _ = st.Project("p1")
	if !reflect.DeepEqual(got.Repos, []string{"/c"}) {
		t.Fatalf("repos not replaced: %v", got.Repos)
	}

	for i, title := range []string{"low", "high"} {
		st.SaveTodo(Todo{ID: title, ProjectID: "p1", Title: title, Priority: i})
	}
	todos, _ := st.Todos("p1")
	if len(todos) != 2 || todos[0].Title != "high" || todos[1].Status != TodoOpen {
		t.Fatalf("todos = %+v", todos)
	}
	if ok, _ := st.ClaimTodo("high"); !ok {
		t.Fatal("first claim must win")
	}
	if ok, _ := st.ClaimTodo("high"); ok {
		t.Fatal("second claim must lose")
	}

	st.Save(Session{ID: "s1", Title: "s", Task: "s", Agent: "claude", Status: StatusDone, ProjectID: "p1", TodoID: "high", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	st.SetSetting(CurrentProjectSetting, "p1")
	if err := st.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if todos, _ := st.Todos("p1"); len(todos) != 0 {
		t.Fatal("todos must be deleted with the project")
	}
	if s, _, _ := st.Get("s1"); s.ProjectID != "" || s.TodoID != "" {
		t.Fatalf("session keeps project link: %+v", s)
	}
	if st.Setting(CurrentProjectSetting) != "" {
		t.Fatal("current project setting must be cleared")
	}
}

func TestMissionsAndNodes(t *testing.T) {
	st := open(t)
	m := Mission{ID: "m1", Title: "BLIK", Goal: "add BLIK", Repo: "/r", Phase: PhaseSpec, State: MissionRunning, TodoID: "t1"}
	if err := st.SaveMission(m); err != nil {
		t.Fatal(err)
	}
	n := Node{ID: "n1", MissionID: "m1", Role: RoleDev, Title: "api", Files: []string{"api/**"}, Deps: []string{"n0"}, Status: NodePending, Position: 3}
	if err := st.SaveNode(n); err != nil {
		t.Fatal(err)
	}
	st.Save(Session{ID: "s1", Title: "x", Task: "x", Agent: "claude", Status: StatusDone, MissionID: "m1", NodeID: "n1", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	got, ok, _ := st.MissionForTodo("t1")
	if !ok || got.Phase != PhaseSpec || !got.Active() {
		t.Fatalf("mission = %+v", got)
	}
	ns, _ := st.Nodes("m1")
	if len(ns) != 1 || ns[0].Files[0] != "api/**" || ns[0].Deps[0] != "n0" || ns[0].Role != RoleDev {
		t.Fatalf("nodes = %+v", ns)
	}
	s, _, _ := st.Get("s1")
	if s.MissionID != "m1" || s.NodeID != "n1" {
		t.Fatalf("session = %+v", s)
	}
	if err := st.DeleteMission("m1"); err != nil {
		t.Fatal(err)
	}
	if ns, _ := st.Nodes("m1"); len(ns) != 0 {
		t.Fatal("nodes must cascade")
	}
	if s, _, _ := st.Get("s1"); s.MissionID != "" {
		t.Fatal("session must be detached")
	}
}
