package pdftext

import "fmt"

// Call stack model.
//
// The extraction is recursive in a few places (object parsing, the page
// tree walk, form XObjects inside form XObjects) and fails with
// RecursionError once a modelled call stack passes recursionLimit = 1000
// frames. Where that error is caught decides the output: inside a form
// XObject the Do handler swallows it (the nested text becomes ""), a
// dictionary turns it into a PdfReadError (so its parent dictionary returns
// the entries read so far), elsewhere the extraction aborts.
//
// Each step is assigned the frame depth it runs at, so nesting fails at
// fixed depths. The numbers below give these limits:
//
//   - a form XObject chain keeps its text up to 489 levels;
//   - a content stream holds up to 496 nested arrays;
//   - a page dictionary 983 /Pages levels deep still parses with nested
//     dictionaries, one at 985 levels does not parse at all.
//
// The model is approximate (the frame count of a step depends on which code
// path is deepest). Every recursion in the package is bounded by it, so
// adversarial input cannot overflow the Go stack.
const (
	frameOpen        = 8  // object reads while the xref is being read
	frameFlattenRoot = 8  // the page tree walk at the root /Pages node
	frameExtractText = 6  // text extraction for a page
	frameMetadata    = 6  // reading the document information dictionary
	frameTitleStr    = 5  // converting the title to text
	frameFormWork    = 16 // frames a form XObject needs beyond its text extraction
)

// extractFrame is the frame of text extraction at a form nesting level
// (0 = the page): each level adds two frames.
func extractFrame(level int) int { return frameExtractText + 2*level }

// Work budget: nothing else limits how often form XObjects are re-run (a
// form that draws another form twice, 30 levels deep, never finishes) or
// the text that accumulates. Past these limits Extract fails instead of
// running for hours or exhausting memory.
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
