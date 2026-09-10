package presets

import (
	"fmt"
	"io/fs"
	"strings"
)

var Names = []string{"coming-soon", "welcome", "newsletter", "launch"}

// Files returns an independent complete, localized workflow starter.
func Files(name string) (map[string][]byte, error) {
	valid := false
	for _, n := range Names {
		if n == name {
			valid = true
		}
	}
	if !valid {
		return nil, fmt.Errorf("unknown preset %q", name)
	}
	files := map[string][]byte{}
	entries, err := fs.ReadDir(FS, "complete")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			b, err := fs.ReadFile(FS, "complete/"+e.Name())
			if err != nil {
				return nil, err
			}
			files[e.Name()] = b
		}
	}
	y := strings.Replace(string(files["mrkt.yaml"]), "name: example", "name: "+name, 1)
	sequence := "sequences:\n  - id: welcome\n    list: newsletter\n    entry: subscription\n    reentry: once\n    steps:\n      - {id: send, type: send, message: welcome, next: done}\n      - {id: done, type: complete}"
	switch name {
	case "coming-soon":
		y = strings.Replace(y, "id: welcome\n    list:", "id: waitlist-welcome\n    list:", 1)
		y = strings.Replace(y, "broadcasts:\n  - {id: news, event: article.published, list: newsletter, message: welcome}", "broadcasts: []", 1)
	case "welcome":
		y = strings.Replace(y, "broadcasts:\n  - {id: news, event: article.published, list: newsletter, message: welcome}", "broadcasts: []", 1)
	case "newsletter":
		y = strings.Replace(y, sequence, "sequences: []", 1)
	case "launch":
		y = strings.Replace(y, sequence, "sequences: []", 1)
		y = strings.Replace(y, "id: news, event: article.published", "id: launch-announcement, event: product.launched", 1)
	}
	files["mrkt.yaml"] = []byte(y)
	type copy struct{ subject, heading, body, unsubscribe string }
	content := map[string]map[string]copy{
		"coming-soon": {
			"en": {"You’re on the waitlist, {{.Name}}", "Your place is saved", "We’ll let you know when early access opens.", "Leave the waitlist"},
			"it": {"Sei nella lista d’attesa, {{.Name}}", "Il tuo posto è riservato", "Ti avviseremo quando aprirà l’accesso anticipato.", "Lascia la lista d’attesa"},
			"tr": {"Bekleme listesindesiniz, {{.Name}}", "Yeriniz ayrıldı", "Erken erişim açıldığında size haber vereceğiz.", "Bekleme listesinden çık"},
		},
		"welcome": {
			"en": {"Welcome aboard, {{.Name}}", "Welcome", "Here’s everything you need to get started.", "Unsubscribe"},
			"it": {"Benvenuto, {{.Name}}", "Benvenuto", "Ecco tutto ciò che ti serve per iniziare.", "Annulla iscrizione"},
			"tr": {"Hoş geldiniz, {{.Name}}", "Hoş geldiniz", "Başlamak için ihtiyacınız olan her şey burada.", "Abonelikten çık"},
		},
		"newsletter": {
			"en": {"This month’s field notes", "A useful update for you", "Fresh product lessons, practical links, and what we’re learning.", "Unsubscribe"},
			"it": {"Gli appunti di questo mese", "Un aggiornamento utile per te", "Novità sul prodotto, link pratici e ciò che stiamo imparando.", "Annulla iscrizione"},
			"tr": {"Bu ayın saha notları", "Sizin için faydalı bir güncelleme", "Yeni ürün dersleri, pratik bağlantılar ve öğrendiklerimiz.", "Abonelikten çık"},
		},
		"launch": {
			"en": {"We’ve launched", "It’s live", "The new release is ready. See what changed and try it today.", "Unsubscribe"},
			"it": {"Abbiamo lanciato", "È online", "La nuova versione è pronta. Scopri le novità e provala oggi.", "Annulla iscrizione"},
			"tr": {"Yayına çıktık", "Artık yayında", "Yeni sürüm hazır. Yenilikleri görün ve bugün deneyin.", "Abonelikten çık"},
		},
	}
	for _, loc := range []string{"en", "it", "tr"} {
		c := content[name][loc]
		files["welcome."+loc+".subject"] = []byte(c.subject + "\n")
		files["welcome."+loc+".txt"] = []byte(c.heading + "\n\n" + c.body + "\n\n" + c.unsubscribe + ": {{.UnsubscribeURL}}\n")
		files["welcome."+loc+".html"] = []byte(`<!doctype html><html lang="` + loc + `"><body><h1>` + c.heading + `</h1><p>` + c.body + `</p><p><a href="{{.UnsubscribeURL}}">` + c.unsubscribe + `</a></p></body></html>` + "\n")
	}
	return files, nil
}
