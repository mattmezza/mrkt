package httpapi

import (
	"encoding/json"
	"github.com/mattmezza/mrkt/internal/engine"
	"net/http"
	"strings"
)

func (s *Server) publicPage(w http.ResponseWriter, r *http.Request, action, project, token string) {
	if len(token) > 512 {
		http.Error(w, "Invalid token", 400)
		return
	}
	input, _ := json.Marshal(map[string]string{"token": token})
	result, err := s.cfg.App.Do(r.Context(), engine.Authority{Public: true, Project: project}, engine.Operation{Project: project, Resource: "consent", Action: "preferences", Input: input})
	if err != nil {
		fail(w, err)
		return
	}
	b, _ := json.Marshal(result)
	var prefs struct {
		Locale string           `json:"locale"`
		Lists  []map[string]any `json:"lists"`
	}
	_ = json.Unmarshal(b, &prefs)
	locale := strings.Split(prefs.Locale, "-")[0]
	if locale != "it" && locale != "tr" {
		locale = "en"
	}
	copy := map[string]map[string]string{
		"en": {"confirm": "Confirm your subscription", "manage": "Manage your subscription", "intro": "You control which messages you receive. Unsubscribing stops future marketing messages; a message already submitted cannot be recalled.", "confirm_button": "Confirm subscription", "unsubscribe_button": "Unsubscribe from this list", "all": "Unsubscribe from all project lists", "preferences": "Your lists"},
		"it": {"confirm": "Conferma la tua iscrizione", "manage": "Gestisci la tua iscrizione", "intro": "Scegli quali messaggi ricevere. La disiscrizione interrompe i futuri messaggi promozionali; quelli già inviati non possono essere richiamati.", "confirm_button": "Conferma iscrizione", "unsubscribe_button": "Disiscriviti da questa lista", "all": "Disiscriviti da tutte le liste", "preferences": "Le tue liste"},
		"tr": {"confirm": "Aboneliğinizi onaylayın", "manage": "Aboneliğinizi yönetin", "intro": "Hangi iletileri alacağınızı siz seçersiniz. Abonelikten çıkmak gelecekteki pazarlama iletilerini durdurur; gönderilmiş bir ileti geri alınamaz.", "confirm_button": "Aboneliği onayla", "unsubscribe_button": "Bu listeden çık", "all": "Projedeki tüm listelerden çık", "preferences": "Listeleriniz"},
	}[locale]
	title := copy["manage"]
	if action == "confirm" {
		title = copy["confirm"]
	}
	if action == "preferences" {
		action = "unsubscribe"
	}
	data := map[string]any{"Title": title, "Locale": locale, "Project": project, "Token": token, "Action": action, "Copy": copy, "Lists": prefs.Lists}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.ExecuteTemplate(w, "public", data)
}

func (s *Server) publicResult(w http.ResponseWriter, locale, action string) {
	locale = strings.Split(locale, "-")[0]
	if locale != "it" && locale != "tr" {
		locale = "en"
	}
	wording := map[string][2]string{"en": {"Subscription confirmed", "You are unsubscribed"}, "it": {"Iscrizione confermata", "Disiscrizione completata"}, "tr": {"Abonelik onaylandı", "Abonelikten çıkıldı"}}[locale]
	index := 0
	if action == "unsubscribe" {
		index = 1
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.ExecuteTemplate(w, "public-result", map[string]any{"Title": wording[index], "Locale": locale})
}
