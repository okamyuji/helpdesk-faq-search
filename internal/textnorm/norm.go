// Package textnorm normalizes text before it is matched by keyword search.
package textnorm

import "golang.org/x/text/unicode/norm"

// NFKC folds full-width and compatibility characters (ＰＣ, ｶﾅ, ①) into their standard forms.
func NFKC(s string) string {
	return norm.NFKC.String(s)
}
