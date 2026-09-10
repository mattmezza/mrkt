package engine

import (
	"context"
	"encoding/json"
	"github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/manifest"
	"github.com/mattmezza/mrkt/internal/security"
	"testing"
	"time"
)

type orderedBlockingSender struct{ started, release chan struct{} }

func (s *orderedBlockingSender) Send(context.Context, mail.Message) (mail.Result, error) {
	close(s.started)
	<-s.release
	return mail.Result{State: mail.StateAccepted}, nil
}

func TestEventAudienceSemanticsAndCompletionOnce(t *testing.T) {
	ctx := context.Background()
	e, a := riskEngine(t)
	defer e.Close()
	now := e.now().UTC().Format(time.RFC3339Nano)
	m := manifest.Manifest{Version: 1, Project: manifest.Project{Name: "p1", DefaultLocale: "en", Locales: []string{"en"}}, Lists: []manifest.List{{ID: "news", Name: "News", Purpose: "News", PolicyVersion: "v1"}}, Sequences: []manifest.Sequence{{ID: "journey", List: "news", Event: "article.published", Entry: "event", Reentry: "once", Steps: []manifest.Step{{ID: "done", Type: "complete"}}}}, Broadcasts: []manifest.Broadcast{{ID: "announcement", Event: "article.published", List: "news", Message: "unused"}}}
	rawManifest, _ := json.Marshal(m)
	_, _ = e.db.Exec(`INSERT INTO releases(id,project_id,digest,manifest,status,created_at) VALUES('rel','p1','digest',?,'active',?)`, string(rawManifest), now)
	_, _ = e.db.Exec(`UPDATE projects SET active_release_id='rel' WHERE id='p1'`)
	_, _ = e.db.Exec(`INSERT INTO contacts(id,project_id,email,attributes,created_at,updated_at) VALUES('contact','p1','a@example.test','{}',?,?)`, now, now)
	if _, er := e.Do(ctx, a, Operation{Project: "p1", Resource: "events", Action: "create", Input: raw(map[string]any{"key": "contact-event", "type": "article.published", "contact_id": "contact", "payload": map[string]any{"title": "one"}})}); er != nil {
		t.Fatal(er)
	}
	var enrollments, broadcasts int
	_ = e.db.QueryRow(`SELECT count(*) FROM enrollments`).Scan(&enrollments)
	_ = e.db.QueryRow(`SELECT count(*) FROM broadcasts`).Scan(&broadcasts)
	if enrollments != 1 || broadcasts != 0 {
		t.Fatalf("contact event enrolled=%d broadcasts=%d", enrollments, broadcasts)
	}
	if _, er := e.Do(ctx, a, Operation{Project: "p1", Resource: "events", Action: "create", Input: raw(map[string]any{"key": "project-event", "type": "article.published", "payload": map[string]any{"title": "two"}})}); er != nil {
		t.Fatal(er)
	}
	_ = e.db.QueryRow(`SELECT count(*) FROM enrollments`).Scan(&enrollments)
	_ = e.db.QueryRow(`SELECT count(*) FROM broadcasts`).Scan(&broadcasts)
	if enrollments != 1 || broadcasts != 1 {
		t.Fatalf("project event enrolled=%d broadcasts=%d", enrollments, broadcasts)
	}
	var enrollmentID string
	_ = e.db.QueryRow(`SELECT id FROM enrollments`).Scan(&enrollmentID)
	if er := e.completeEnrollment(ctx, "p1", enrollmentID, "test"); er != nil {
		t.Fatal(er)
	}
	if er := e.completeEnrollment(ctx, "p1", enrollmentID, "duplicate"); er != nil {
		t.Fatal(er)
	}
	var emitted int
	_ = e.db.QueryRow(`SELECT count(*) FROM outbox WHERE type='sequence.completed' AND correlation_id=?`, enrollmentID).Scan(&emitted)
	if emitted != 1 {
		t.Fatalf("completion outbox=%d", emitted)
	}
}

func TestFeedbackDuringBlockedSMTPKeepsComplaintAndCancellation(t *testing.T) {
	ctx := context.Background()
	e := registryFixture(t)
	a := Authority{Admin: true}
	created, er := e.Do(ctx, a, Operation{Project: "one", Resource: "transports", Action: "create", Input: raw(map[string]any{"name": "ordered", "config": map[string]any{"host": "localhost", "port": 1025, "tls_mode": "plain", "from": "no-reply@example.test", "allow_private": true}})})
	if er != nil {
		t.Fatal(er)
	}
	tid := created.(map[string]any)["id"].(string)
	secret := created.(map[string]any)["secret"].(string)
	now := e.now().UTC().Format(time.RFC3339Nano)
	_, _ = e.db.Exec(`INSERT INTO contacts(id,project_id,email,attributes,created_at,updated_at) VALUES('ordered-contact','one','ordered@example.test','{}',?,?)`, now, now)
	_, _ = e.db.Exec(`INSERT INTO enrollments(id,project_id,contact_id,release_id,sequence_id,current_step,state,created_at,updated_at,entry_key) VALUES('ordered-enrollment','one','ordered-contact','release','sequence','send','waiting',?,?,'once')`, now, now)
	_, _ = e.db.Exec(`INSERT INTO deliveries(id,project_id,contact_id,release_id,enrollment_id,step_id,message_key,message_id,state,created_at,updated_at) VALUES('ordered-delivery','one','ordered-contact','release','ordered-enrollment','send','message','ordered-message','dispatching',?,?)`, now, now)
	sender := &orderedBlockingSender{make(chan struct{}), make(chan struct{})}
	effective := make(chan string, 1)
	go func() {
		res, _ := sender.Send(ctx, mail.Message{})
		effective <- e.recordSMTPResult(ctx, "ordered-delivery", res.State, res.Detail)
	}()
	<-sender.started
	body := raw(normalizedFeedback{ID: "ordered-complaint", Type: "complaint", MessageID: "ordered-message", Email: "ordered@example.test", OccurredAt: now})
	timestamp := e.now().Unix()
	if _, er = e.doFeedback(ctx, Operation{Project: "one", ID: tid, Input: raw(feedbackInput{Body: body, Timestamp: timestamp, Signature: security.SignWebhook([]byte(secret), body, timestamp)})}); er != nil {
		t.Fatal(er)
	}
	close(sender.release)
	if got := <-effective; got != "complaint" {
		t.Fatalf("effective state=%s", got)
	}
	var delivery, enrollment string
	_ = e.db.QueryRow(`SELECT state FROM deliveries WHERE id='ordered-delivery'`).Scan(&delivery)
	_ = e.db.QueryRow(`SELECT state FROM enrollments WHERE id='ordered-enrollment'`).Scan(&enrollment)
	if delivery != "complaint" || enrollment != "cancelled" {
		t.Fatalf("delivery=%s enrollment=%s", delivery, enrollment)
	}
}

func TestBroadcastCompletesAndEmitsOnceAfterAudienceJobsTerminal(t *testing.T) {
	ctx := context.Background()
	e, _ := riskEngine(t)
	defer e.Close()
	now := e.now().UTC().Format(time.RFC3339Nano)
	_, _ = e.db.Exec(`INSERT INTO broadcasts(id,project_id,release_id,definition_id,state,audience_frozen_at,created_at) VALUES('complete-broadcast','p1','release','definition','queued',?,?)`, now, now)
	_, _ = e.db.Exec(`INSERT INTO jobs(id,project_id,kind,payload,run_at,state,created_at,updated_at) VALUES('terminal-job','p1','broadcast','{"broadcast_id":"complete-broadcast"}',?,'complete',?,?)`, now, now, now)
	if er := e.maybeCompleteBroadcast(ctx, "p1", "complete-broadcast"); er != nil {
		t.Fatal(er)
	}
	if er := e.maybeCompleteBroadcast(ctx, "p1", "complete-broadcast"); er != nil {
		t.Fatal(er)
	}
	var state string
	var emitted int
	_ = e.db.QueryRow(`SELECT state FROM broadcasts WHERE id='complete-broadcast'`).Scan(&state)
	_ = e.db.QueryRow(`SELECT count(*) FROM outbox WHERE type='broadcast.completed' AND correlation_id='complete-broadcast'`).Scan(&emitted)
	if state != "completed" || emitted != 1 {
		t.Fatalf("state=%s emitted=%d", state, emitted)
	}
}

func TestPausedTerminalDelayDoesNotCompleteUntilResumed(t *testing.T) {
	ctx := context.Background()
	e, _ := riskEngine(t)
	defer e.Close()
	now := e.now().UTC().Format(time.RFC3339Nano)
	_, _ = e.db.Exec(`INSERT INTO contacts(id,project_id,email,attributes,created_at,updated_at) VALUES('delay-contact','p1','delay@example.test','{}',?,?)`, now, now)
	_, _ = e.db.Exec(`INSERT INTO enrollments(id,project_id,contact_id,release_id,sequence_id,current_step,state,created_at,updated_at,entry_key) VALUES('delay-enrollment','p1','delay-contact','release','sequence','delay','paused',?,?,'once')`, now, now)
	payload, _ := json.Marshal(map[string]string{"enrollment_id": "delay-enrollment", "reason": "terminal delay"})
	_, _ = e.db.Exec(`INSERT INTO jobs(id,project_id,kind,payload,run_at,state,created_at,updated_at) VALUES('delay-job','p1','enrollment_complete',? ,?,'dispatching',?,?)`, string(payload), now, now, now)
	job := claimedJob{id: "delay-job", p: "p1", kind: "enrollment_complete", payload: string(payload)}
	if er := e.runJob(ctx, job); er != nil {
		t.Fatal(er)
	}
	var enrollmentState, jobState string
	_ = e.db.QueryRow(`SELECT state FROM enrollments WHERE id='delay-enrollment'`).Scan(&enrollmentState)
	_ = e.db.QueryRow(`SELECT state FROM jobs WHERE id='delay-job'`).Scan(&jobState)
	if enrollmentState != "paused" || jobState != "pending" {
		t.Fatalf("paused completion enrollment=%s job=%s", enrollmentState, jobState)
	}
	_, _ = e.db.Exec(`UPDATE enrollments SET state='waiting' WHERE id='delay-enrollment'`)
	_, _ = e.db.Exec(`UPDATE jobs SET state='dispatching' WHERE id='delay-job'`)
	if er := e.runJob(ctx, job); er != nil {
		t.Fatal(er)
	}
	_ = e.db.QueryRow(`SELECT state FROM enrollments WHERE id='delay-enrollment'`).Scan(&enrollmentState)
	var emitted int
	_ = e.db.QueryRow(`SELECT count(*) FROM outbox WHERE type='sequence.completed' AND correlation_id='delay-enrollment'`).Scan(&emitted)
	if enrollmentState != "completed" || emitted != 1 {
		t.Fatalf("resumed completion enrollment=%s emitted=%d", enrollmentState, emitted)
	}
}

func TestBroadcastAndEnrollmentStateBlockIntent(t *testing.T) {
	ctx := context.Background()
	e, a := riskEngine(t)
	defer e.Close()
	now := e.now().UTC().Format(time.RFC3339Nano)
	_, _ = e.db.Exec(`INSERT INTO contacts(id,project_id,email,attributes,created_at,updated_at) VALUES('c','p1','c@example.test','{}',?,?)`, now, now)
	_, _ = e.db.Exec(`INSERT INTO lists(project_id,id,name,purpose,policy_version,release_id) VALUES('p1','news','News','News','v1','r')`)
	_, _ = e.db.Exec(`INSERT INTO consent(id,project_id,contact_id,list_id,state,source,policy_version,requested_at,updated_at) VALUES('cons','p1','c','news','confirmed','test','v1',?,?)`, now, now)
	_, _ = e.db.Exec(`INSERT INTO broadcasts(id,project_id,release_id,definition_id,state,audience_frozen_at,created_at) VALUES('b','p1','r','def','queued',?,?)`, now, now)
	if _, er := e.Do(ctx, a, Operation{Project: "p1", Resource: "broadcasts", ID: "b", Action: "pause"}); er != nil {
		t.Fatal(er)
	}
	_, _, _, result, er := e.reserveDelivery(ctx, "p1", "c@example.test", "c", "news", "r", "", "b", "broadcast", "m", "hash", "__installation__", "", nil, now)
	if er != nil || result != "ineligible" {
		t.Fatalf("paused broadcast reservation=%s %v", result, er)
	}
	if _, er = e.Do(ctx, a, Operation{Project: "p1", Resource: "broadcasts", ID: "b", Action: "resume"}); er != nil {
		t.Fatal(er)
	}
	if _, er = e.Do(ctx, a, Operation{Project: "p1", Resource: "broadcasts", ID: "b", Action: "cancel"}); er != nil {
		t.Fatal(er)
	}
	var audits int
	_ = e.db.QueryRow(`SELECT count(*) FROM audit WHERE project_id='p1' AND action LIKE 'broadcast.%'`).Scan(&audits)
	if audits != 3 {
		t.Fatalf("broadcast audits=%d", audits)
	}
	_, _ = e.db.Exec(`INSERT INTO enrollments(id,project_id,contact_id,release_id,sequence_id,current_step,state,created_at,updated_at,entry_key) VALUES('e','p1','c','r','seq','send','cancelled',?,?,'once')`, now, now)
	_, _, _, result, er = e.reserveDelivery(ctx, "p1", "c@example.test", "c", "news", "r", "e", "", "send", "m", "hash", "__installation__", "", nil, now)
	if er != nil || result != "ineligible" {
		t.Fatalf("cancelled enrollment reservation=%s %v", result, er)
	}
}
