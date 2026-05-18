package export

import (
	"encoding/csv"
	"io"

	"okeyparser/internal/parser"
)

func WriteCSV(w io.Writer, products []parser.Product) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"category", "subcategory", "name", "price", "url"}); err != nil {
		return err
	}
	for _, product := range products {
		if err := writer.Write([]string{product.Category, product.Subcategory, product.Name, product.Price, product.URL}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}
