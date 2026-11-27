package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestTaskInputValidation(t *testing.T) {
	in := taskCreate{ProjectID: "00000000-0000-4000-8000-000000000011", Name: "Task", Stages: []stageCreate{{"S1", 1}}}
	if !in.valid() {
		t.Fatal("valid task denied")
	}
	for _, stages := range [][]stageCreate{nil, {{"S1", 0}}, {{"bad/id", 1}}, {{"S1", 1}, {"S1", 2}}} {
		bad := in
		bad.Stages = stages
		if bad.valid() {
			t.Fatal("invalid stages accepted", stages)
		}
	}
}

func TestPostgresTaskIntegration(t *testing.T) {
	ownerDSN, authDSN, appDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN"), os.Getenv("BROWSEAI_TEST_AUTH_DSN"), os.Getenv("BROWSEAI_TEST_APP_DSN")
	if ownerDSN == "" && authDSN == "" && appDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || authDSN == "" || appDSN == "" {
		t.Fatal("incomplete integration configuration")
	}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, authDB, appDB := open(ownerDSN), open(authDSN), open(appDSN)
	hash, err := hashPassword("synthetic-password-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("UPDATE browseai.users SET password_hash=$1,enabled=true WHERE login IN ('mock-a','mock-b')", hash); err != nil {
		t.Fatal(err)
	}
	a, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		t.Fatal(err)
	}
	h := projectRoutes(a, authDB, appDB)
	request := func(method, path, body string, cookie *http.Cookie, want int) *http.Response {
		w := authRequest(h, method, path, testOrigin, body, "192.0.2.8", cookie).Result()
		t.Cleanup(func() { w.Body.Close() })
		if w.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d", method, path, w.StatusCode, want)
		}
		return w
	}
	login := func(name string) *http.Cookie {
		return request("POST", "/api/auth/login", `{"login":"`+name+`","password":"synthetic-password-only"}`, nil, 204).Cookies()[0]
	}
	ca, cb := login("mock-a"), login("mock-b")
	const projectID = "00000000-0000-4000-8000-000000000011"
	const otherProjectID = "00000000-0000-4000-8000-000000000012"
	body := func(project string) string {
		return `{"project_id":"` + project + `","name":"Test task","stages":[{"id":"S1","weight":1},{"id":"S2","weight":2}],"max_repairs":1}`
	}
	request("GET", "/api/tasks", "", nil, 401)
	request("POST", "/api/tasks", body(otherProjectID), ca, 404)
	request("POST", "/api/tasks", `{"project_id":"`+projectID+`","name":"Bad","stages":[{"id":"S1","weight":0}]}`, ca, 400)
	request("POST", "/api/tasks", `{"project_id":"`+projectID+`","name":"Bad","state":"COMPLETED"}`, ca, 400)
	create := func() taskRecord {
		res := request("POST", "/api/tasks", body(projectID), ca, 201)
		var r taskRecord
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil || r.State != taskQueued || r.Count != 2 || r.Revision != 0 {
			t.Fatal("invalid task", r, err)
		}
		return r
	}
	draft := create()
	request("DELETE", "/api/tasks/"+draft.ID+"?revision=0", "", cb, 404)
	request("DELETE", "/api/tasks/"+draft.ID+"?revision=0", "", ca, 204)
	request("GET", "/api/tasks/"+draft.ID, "", ca, 404)
	r := create()
	path := "/api/tasks/" + r.ID
	request("GET", path, "", cb, 404)
	request("PATCH", path, `{"name":"stolen","revision":0}`, cb, 404)
	request("POST", path+"/pause", `{"revision":0}`, cb, 404)
	request("PATCH", path, `{"name":"renamed","revision":0}`, ca, 200)
	request("PATCH", path, `{"name":"stale","revision":0}`, ca, 409)
	request("DELETE", path+"?revision=1", "", ca, 409)
	request("POST", path+"/pause", `{"revision":1,"CIAllPassed":true}`, ca, 400)
	request("POST", path+"/pause", `{"revision":1}`, ca, 200)
	// The internal service is the only place where reconciled evidence may be supplied.
	p, err := a.sessions.authenticate(context.Background(), ca.Value)
	if err != nil {
		t.Fatal(err)
	}
	s := taskStore{db: appDB}
	next, err := s.transition(context.Background(), p, r.ID, 2, actionResume, taskEvidence{ManualResume: true, Reconciled: true})
	if err != nil || next.State != taskQueued || next.Revision != 3 {
		t.Fatal("persisted resume", next, err)
	}
	// Two requests with the same revision cannot both write.
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			codes <- authRequest(h, "POST", path+"/stop", testOrigin, `{"revision":3}`, "192.0.2.8", ca).Code
		}()
	}
	first, second := <-codes, <-codes
	if !((first == 200 && second == 409) || (first == 409 && second == 200)) {
		t.Fatal("concurrent transition", first, second)
	}
	request("POST", path+"/pause", `{"revision":4}`, ca, 409)
	request("DELETE", path+"?revision=4", "", ca, 409)
	res := request("GET", path, "", ca, 200)
	var final taskRecord
	if err := json.NewDecoder(res.Body).Decode(&final); err != nil || final.State != taskStopped || final.Revision != 4 {
		t.Fatal("terminal state was not persisted", final, err)
	}
	res = request("GET", "/api/tasks?project_id="+projectID, "", cb, 200)
	var list struct {
		Tasks []taskRecord `json:"tasks"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil || len(list.Tasks) != 0 {
		t.Fatal("cross-tenant list", list, err)
	}
}
