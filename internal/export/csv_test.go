package export

import (
	"strings"
	"testing"

	"okeyparser/internal/parser"
)

func TestWriteCSVIncludesHeaderAndEscapesValues(t *testing.T) {
	var out strings.Builder
	products := []parser.Product{
		{Category: "dairy", Subcategory: "milk", Name: "Молоко, 3,2%", Price: "89 ₽", URL: "https://example.test/milk"},
	}

	if err := WriteCSV(&out, products); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	want := "category,subcategory,name,price,url\n" +
		"dairy,milk,\"Молоко, 3,2%\",89 ₽,https://example.test/milk\n"
	if out.String() != want {
		t.Fatalf("csv mismatch:\nwant %q\ngot  %q", want, out.String())
	}
}
