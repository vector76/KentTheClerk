package pdftext

// Port of ContentStream._parse_content_stream (with forced_encoding "bytes",
// so every string operand stays a byte string).

type operation struct {
	operands []Object
	operator string
}

func isASCIIAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// parseContent parses a content stream; frame is the Python frame of its
// read_object calls.
func parseContent(data []byte, frame int) ([]operation, error) {
	p := &parser{data: data, frame: frame}
	var ops []operation
	var operands []Object
	for {
		c := p.readNonWhitespace()
		if c < 0 {
			break
		}
		p.back(1)
		b := byte(c)
		switch {
		case isASCIIAlpha(b) || b == '\'' || b == '"':
			if err := p.tokenLimit(isNameDelim, 128); err != nil {
				return nil, err
			}
			op := string(p.readUntilDelim())
			if op == "BI" {
				if len(operands) != 0 {
					return nil, pyErr("AssertionError", "operands before BI")
				}
				if err := p.readInlineImage(); err != nil {
					return nil, err
				}
				ops = append(ops, operation{operator: "INLINE IMAGE"})
			} else {
				ops = append(ops, operation{operands: operands, operator: op})
				operands = nil
			}
		case b == '%':
			for {
				c := p.read1()
				if c < 0 || c == '\r' || c == '\n' {
					break
				}
			}
		default:
			o, err := p.readObject()
			if err != nil {
				return nil, err
			}
			operands = append(operands, o)
		}
	}
	return ops, nil
}
