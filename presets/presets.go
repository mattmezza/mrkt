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
	labels := map[string]string{"coming-soon": "You’re on the waitlist", "welcome": "Welcome aboard", "newsletter": "Your newsletter", "launch": "We’ve launched"}
	for _, loc := range []string{"en", "it", "tr"} {
		files["welcome."+loc+".subject"] = []byte(labels[name] + ", {{.Name}}\n")
	}
	return files, nil
}
