package i18n

import (
	"bytes"
	"encoding/json"
	"testing"
)

// json.Unmarshal no avisa si una clave se repite: se queda con la última.
// Si las dos copias tienen textos distintos, la primera se pierde sin error.
// Por eso se recorren los tokens del archivo uno por uno.
func TestTranslationFilesHaveNoDuplicateKeys(t *testing.T) {
	for _, file := range []string{"en.json", "es.json"} {
		t.Run(file, func(t *testing.T) {
			data, err := translationFiles.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			for _, key := range duplicateKeys(t, data) {
				t.Errorf("%s: key %q appears more than once", file, key)
			}
		})
	}
}

// duplicateKeys devuelve las claves repetidas de un objeto JSON plano
// (clave → texto), que es la forma de los archivos de idioma.
func duplicateKeys(t *testing.T, data []byte) []string {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("expected a JSON object, got %v (err %v)", tok, err)
	}

	seen := make(map[string]bool)
	var dups []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		key, ok := tok.(string)
		if !ok {
			t.Fatalf("expected a string key, got %v", tok)
		}
		if seen[key] {
			dups = append(dups, key)
		}
		seen[key] = true

		var value string
		if err := dec.Decode(&value); err != nil {
			t.Fatalf("key %q: value must be a string: %v", key, err)
		}
	}
	return dups
}
