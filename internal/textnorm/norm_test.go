package textnorm

import "testing"

func TestNFKCFoldsFullWidth(t *testing.T) {
	cases := map[string]string{
		"ＰＣ　Ｗｉ－Ｆｉ": "PC Wi-Fi",
		"ｶﾀｶﾅ":     "カタカナ",
		"①":        "1",
		"":         "",
	}
	for in, want := range cases {
		if got := NFKC(in); got != want {
			t.Errorf("NFKC(%q) = %q, want %q", in, got, want)
		}
	}
}
