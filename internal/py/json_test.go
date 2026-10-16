package py

import "testing"

// TestJSON checks Loads and Dumps on cases that include each decode error: for a valid document the
// re-encoded text, for an invalid one the error message.
func TestJSON(t *testing.T) {
	for _, c := range []struct {
		in, want string
		isErr    bool
	}{
		{"\"\\u", "Invalid \\uXXXX escape: line 1 column 3 (char 2)", true},
		{"\"\\u1", "Invalid \\uXXXX escape: line 1 column 3 (char 2)", true},
		{"\"\\u123", "Invalid \\uXXXX escape: line 1 column 3 (char 2)", true},
		{"\"\\ud800\\u12", "Invalid \\uXXXX escape: line 1 column 9 (char 8)", true},
		{"\"\\ud800\\u", "Invalid \\uXXXX escape: line 1 column 9 (char 8)", true},
		{"{\"a\":1,}", "Illegal trailing comma before end of object: line 1 column 7 (char 6)", true},
		{"[1,]", "Illegal trailing comma before end of array: line 1 column 3 (char 2)", true},
		{"{\"a\" 1}", "Expecting ':' delimiter: line 1 column 6 (char 5)", true},
		{"{\"a\":1 \"b\"}", "Expecting ',' delimiter: line 1 column 8 (char 7)", true},
		{"[1 2]", "Expecting ',' delimiter: line 1 column 4 (char 3)", true},
		{"\"abc", "Unterminated string starting at: line 1 column 1 (char 0)", true},
		{"\"a\\x\"", "Invalid \\escape: line 1 column 3 (char 2)", true},
		{"\"\\u12\"", "Invalid \\uXXXX escape: line 1 column 3 (char 2)", true},
		{"\"\\u12zz\"", "Invalid \\uXXXX escape: line 1 column 3 (char 2)", true},
		{"\"a\x01\"", "Invalid control character at: line 1 column 3 (char 2)", true},
		{"", "Expecting value: line 1 column 1 (char 0)", true},
		{" ", "Expecting value: line 1 column 2 (char 1)", true},
		{"{", "Expecting property name enclosed in double quotes: line 1 column 2 (char 1)", true},
		{"[", "Expecting value: line 1 column 2 (char 1)", true},
		{"{\"a\":", "Expecting value: line 1 column 6 (char 5)", true},
		{"tru", "Expecting value: line 1 column 1 (char 0)", true},
		{"1 2", "Extra data: line 1 column 3 (char 2)", true},
		{"-", "Expecting value: line 1 column 1 (char 0)", true},
		{"01", "Extra data: line 1 column 2 (char 1)", true},
		{"1.", "Extra data: line 1 column 2 (char 1)", true},
		{"1e", "Extra data: line 1 column 2 (char 1)", true},
		{"{\"a\":1}\n{\"b\":2}", "Extra data: line 2 column 1 (char 8)", true},
		{"\ufeff{}", "Unexpected UTF-8 BOM (decode using utf-8-sig): line 1 column 1 (char 0)", true},
		{"{\"a\":1,\"a\":2}", "{\"a\": 2}", false},
		{"-0", "0", false},
		{"1E400", "Infinity", false},
		{"[1.5e-7, 1e16, 2.0, 1e22, 12345678901234567890, 0.1, 100.0, -0.0, 1e-5, 0.0001]", "[1.5e-07, 1e+16, 2.0, 1e+22, 12345678901234567890, 0.1, 100.0, -0.0, 1e-05, 0.0001]", false},
		{"{\"é\": \"\\u00e9\\n\\u001b\\u007f\\u2028<>&\"}", "{\"é\": \"é\\n\\u001b\x7f\u2028<>&\"}", false},
		{"[]", "[]", false},
		{"{}", "{}", false},
		{"{\"a\": [], \"b\": {}}", "{\"a\": [], \"b\": {}}", false},
		{"\n\n  {\"x\" : 1 , }", "Illegal trailing comma before end of object: line 3 column 12 (char 13)", true},
		{"\"\\ud83d\\ude00\"", "\"😀\"", false},
		{"[1,\n2,\n,3]", "Expecting value: line 3 column 1 (char 7)", true},
	} {
		v, err := Loads(c.in)
		var got string
		if err != nil {
			got = err.Error()
		} else {
			got = Dumps(v)
		}
		if (err != nil) != c.isErr || got != c.want {
			t.Errorf("%q: got %q (error %v), want %q (error %v)", c.in, got, err != nil, c.want, c.isErr)
		}
	}
}

func TestFloatRepr(t *testing.T) {
	for f, want := range map[float64]string{1e16: "1e+16", 1e-5: "1e-05", 0.0001: "0.0001", 123.456: "123.456",
		100: "100.0", 1.5e300: "1.5e+300", 0.1: "0.1", 12345678901234567.0: "1.2345678901234568e+16"} {
		if got := FloatRepr(f); got != want {
			t.Errorf("%v: got %s want %s", f, got, want)
		}
	}
}

func TestRepr(t *testing.T) {
	for in, want := range map[string]string{"a'b": `"a'b"`, `a"b`: `'a"b'`, `a'"b`: `'a\'"b'`, "x\ny\x01\u00a0é\u200b": `'x\ny\x01\xa0é\u200b'`} {
		if got := StrRepr(in); got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
}
