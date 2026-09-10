package manifest

import (
	"errors"
	"fmt"
	"github.com/vanng822/go-premailer/premailer"
	"golang.org/x/net/html"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"io"
	"math"
	"strings"
	"time"
	_ "time/tzdata"
)

func renderFunctions() map[string]any {
	return map[string]any{
		"number": func(value float64, locale string, decimals int) (string, error) {
			if decimals < 0 || decimals > 6 || math.IsNaN(value) || math.IsInf(value, 0) {
				return "", errors.New("number requires finite value and 0–6 decimals")
			}
			return message.NewPrinter(language.Make(locale)).Sprintf("%.*f", decimals, value), nil
		},
		"date": func(value, locale, timezone string) (string, error) {
			t, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return "", err
			}
			zone, err := time.LoadLocation(timezone)
			if err != nil {
				return "", err
			}
			t = t.In(zone)
			switch strings.Split(locale, "-")[0] {
			case "it":
				return t.Format("02/01/2006 15:04 MST"), nil
			case "tr":
				return t.Format("02.01.2006 15:04 MST"), nil
			default:
				return t.Format("Jan 2, 2006 3:04 PM MST"), nil
			}
		},
	}
}

// Inline only embedded CSS. This path never loads linked CSS, URLs, files or images.
func inlineEmailCSS(source string) (string, error) {
	z := html.NewTokenizer(strings.NewReader(source))
	nodes, rules, styleBytes := 0, 0, 0
	inStyle := false
	hasStyle := false
	for {
		typ := z.Next()
		if typ == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", z.Err()
			}
			break
		}
		if typ == html.StartTagToken || typ == html.SelfClosingTagToken {
			nodes++
			if nodes > 5000 {
				return "", errors.New("email exceeds 5000 elements")
			}
			token := z.Token()
			switch token.Data {
			case "script", "iframe", "object", "embed", "link", "base", "form":
				return "", fmt.Errorf("email element %s is not permitted", token.Data)
			case "style":
				inStyle = true
				hasStyle = true
			}
			for _, a := range token.Attr {
				if strings.HasPrefix(strings.ToLower(a.Key), "on") {
					return "", errors.New("event handlers are not permitted in email")
				}
			}
		}
		if typ == html.EndTagToken {
			token := z.Token()
			if token.Data == "style" {
				inStyle = false
			}
		}
		if typ == html.TextToken && inStyle {
			css := string(z.Text())
			styleBytes += len(css)
			rules += strings.Count(css, "{")
			if styleBytes > 16384 || rules > 128 {
				return "", errors.New("email CSS exceeds 16 KiB or 128 rules")
			}
			if strings.Contains(strings.ToLower(css), "@import") {
				return "", errors.New("CSS imports are not permitted")
			}
		}
	}
	if !hasStyle {
		return source, nil
	}
	p, err := premailer.NewPremailerFromString(source, premailer.NewOptions())
	if err != nil {
		return "", err
	}
	result, err := p.Transform()
	if err != nil {
		return "", err
	}
	if len(result) > MaxRenderSize {
		return "", errors.New("inlined email exceeds 1 MiB")
	}
	return result, nil
}
