package pdftext

import "fmt"

// Python stack model.
//
// pypdf is recursive in a few places (object parsing, the page tree walk,
// form XObjects inside form XObjects) and fails with RecursionError once
// Python's stack passes sys.getrecursionlimit() = 1000 frames. Where that
// error is caught decides the output: inside a form XObject the Do handler
// swallows it (the nested text becomes ""), a dictionary turns it into a
// PdfReadError (so its parent dictionary returns the entries read so far),
// elsewhere the extraction aborts.
//
// To fail at the same depth, the port counts the frames pypdf would be
// using. The numbers below are for the call stack of the Python CLI
// (kb.py <module>, main, ingest.run, extract_pdf, then pypdf), and were
// calibrated against pypdf 6.14.2 on CPython 3.13 with generated inputs:
//
//   - a form XObject chain keeps its text up to 489 levels;
//   - a content stream holds up to 496 nested arrays;
//   - a page dictionary 983 /Pages levels deep still parses with nested
//     dictionaries, one at 985 levels does not parse at all.
//
// The model is approximate (the exact frame count depends on which pypdf
// code path is deepest), so results within a few levels of these limits
// can differ from pypdf. Every recursion in the package is bounded by it,
// so adversarial input cannot overflow the Go stack.
const (
	frameOpen        = 8  // caller of get_object while PdfReader() reads the xref
	frameFlattenRoot = 8  // _flatten for the root /Pages node
	frameExtractText = 6  // _extract_text for a page
	frameMetadata    = 6  // reader.metadata -> _info
	frameTitleStr    = 5  // str(title) in extract_pdf
	frameFormWork    = 16 // frames a form XObject needs beyond its _extract_text
)

// extractFrame is the frame of _extract_text for a form nesting level
// (0 = the page): each level adds extract_xform_text and _extract_text.
func extractFrame(level int) int { return frameExtractText + 2*level }

// Work budget: pypdf has no limit on how often form XObjects are re-run
// (a form that draws another form twice, 30 levels deep, never finishes)
// or on the text that accumulates. These limits are a deliberate deviation:
// past them Extract fails instead of running for hours or exhausting memory.
const (
	maxFormRuns    = 250_000    // form XObject executions per document
	maxOperations  = 50_000_000 // content stream operations per document
	maxOutputRunes = 50_000_000 // characters of text produced per document
)

// budgetError aborts the whole extraction; catch() does not intercept it.
type budgetError struct{ msg string }

func (d *Document) spend(forms, ops, runes int) {
	d.formRuns += forms
	d.operations += ops
	d.outputRunes += runes
	switch {
	case d.formRuns > maxFormRuns:
		panic(budgetError{fmt.Sprintf("more than %d form XObject executions", maxFormRuns)})
	case d.operations > maxOperations:
		panic(budgetError{fmt.Sprintf("more than %d content stream operations", maxOperations)})
	case d.outputRunes > maxOutputRunes:
		panic(budgetError{fmt.Sprintf("more than %d characters of text", maxOutputRunes)})
	}
}
