package presets

import (
	"strings"
	"testing"
)

func TestPresetCopyIsPurposeSpecificAndLocalized(t *testing.T) {
	seenEnglish := map[string]bool{}
	for _, name := range Names {
		files, err := Files(name)
		if err != nil {
			t.Fatal(err)
		}
		en := string(files["welcome.en.txt"])
		if seenEnglish[en] {
			t.Fatalf("%s repeats another preset's English body", name)
		}
		seenEnglish[en] = true
		for _, locale := range []string{"en", "it", "tr"} {
			subject := string(files["welcome."+locale+".subject"])
			body := string(files["welcome."+locale+".txt"])
			if strings.TrimSpace(subject) == "" || !strings.Contains(body, "{{.UnsubscribeURL}}") {
				t.Fatalf("%s/%s lacks localized subject or unsubscribe", name, locale)
			}
		}
		if string(files["welcome.en.txt"]) == string(files["welcome.it.txt"]) || string(files["welcome.it.txt"]) == string(files["welcome.tr.txt"]) {
			t.Fatalf("%s locale bodies are not distinct", name)
		}
	}
}
