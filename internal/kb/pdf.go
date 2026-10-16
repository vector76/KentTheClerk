package kb

import (
	"strings"

	"kenttheclerk/internal/pdftext"
	"kenttheclerk/internal/py"
)

// extractPDF is the pdf-v1 extractor: one chunk per page, as pdftext extracts it.
func extractPDF(path string) ([]chunk, any) {
	pages, title, hasTitle, err := pdftext.Extract(path)
	if err != nil {
		py.Raise("PDF error", "%s", err.Error())
	}
	var chunks []chunk
	for n, txt := range pages {
		chunks = append(chunks, chunk{locator: py.D("page", py.IntOf(n+1)), text: strings.Trim(strings.ReplaceAll(txt, "\r\n", "\n"), "\n")})
	}
	var t any
	if hasTitle && title != "" {
		t = py.Strip(title)
	}
	return chunks, t
}
