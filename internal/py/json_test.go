package py

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestJSONAgainstPython compares Loads/Dumps with json.loads/json.dumps(ensure_ascii=False) on cases that
// include each decode error. Skipped when python is not available.
func TestJSONAgainstPython(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not available")
	}
	cmd := exec.Command(py, "-I", "testdata/jsoncases.py")
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var cases [][3]string
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		v, err := Loads(c[0])
		var got [2]string
		if err != nil {
			got = [2]string{"ERR", err.Error()}
		} else {
			got = [2]string{"OK", Dumps(v)}
		}
		if got[0] != c[1] || got[1] != c[2] {
			t.Errorf("%q: got %v, Python %v %q", c[0], got, c[1], c[2])
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
