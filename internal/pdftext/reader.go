package pdftext

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// The file structure, read in non-strict mode: xref tables and streams,
// trailers, object lookup.

// Document is an opened PDF.
type Document struct {
	data       []byte
	xref       map[int]map[int]int
	xrefFree   map[int]map[int]bool
	xrefObjStm map[int][2]int
	trailer    *Dict
	xrefIndex  int
	// xrefIndexBad: the first xref subsection started with a non-number
	xrefIndexBad bool
	cache        map[[2]int]Object
	known        map[[2]int]bool
	root         *Dict
	// frame is the stack frame of the code that would look up an object
	// right now (see frames.go)
	frame int
	// work done so far (see frames.go)
	formRuns, operations, outputRunes int
	xrefOrder                         []int // generations of xref in insertion order
	// searchCache memoizes searchObject
	searchCache map[[2]int][]int
}

// raised carries an error through panics, so that it unwinds to the
// enclosing handler (catch).
type raised struct{ err error }

func raise(err error) { panic(raised{err}) }

// catch runs f, turning a raised error into its result.
func catch(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if rr, ok := r.(raised); ok {
				err = rr.err
				return
			}
			panic(r)
		}
	}()
	return f()
}

func must(o Object, err error) Object {
	if err != nil {
		raise(err)
	}
	return o
}

// resolve follows a reference once (errors are raised). An object whose
// body is itself a reference resolves to that reference, not to its target.
func (d *Document) resolve(o Object) Object {
	r, ok := o.(Ref)
	if !ok {
		return o
	}
	v, err := d.getObject(r)
	if err != nil {
		raise(err)
	}
	return v
}

// resolveOpt returns the value of key in dd resolved, or nil when absent.
func (d *Document) resolveOpt(dd *Dict, key Name) Object {
	v, ok := dd.Get(key)
	if !ok {
		return nil
	}
	return d.resolve(v)
}

func isNullOrNone(d *Document, x Object) bool {
	if x == nil {
		return true
	}
	r := d.resolve(x)
	if r == nil {
		return true
	}
	_, isNull := r.(Null)
	return isNull
}

func openDocument(data []byte) (*Document, error) {
	d := &Document{
		data:       data,
		xref:       map[int]map[int]int{},
		xrefFree:   map[int]map[int]bool{},
		xrefObjStm: map[int][2]int{},
		trailer:    newDict(),
		cache:      map[[2]int]Object{},
		known:      map[[2]int]bool{},
	}
	d.frame = frameOpen
	if err := catch(d.read); err != nil {
		return nil, err
	}
	return d, nil
}

func hasPrefix(b []byte, s string) bool { return bytes.HasPrefix(b, []byte(s)) }

// trimSpaceBytes trims ASCII whitespace from both ends.
func trimSpaceBytes(b []byte) []byte { return bytes.Trim(b, " \t\n\r\x0b\x0c") }

// parseIntLoose parses a signed decimal integer, ignoring surrounding ASCII whitespace.
func parseIntLoose(b []byte) (int64, error) {
	s := string(trimSpaceBytes(b))
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, classErr("ValueError", "invalid literal for int() with base 10: %q", s)
	}
	return v, nil
}

// readPreviousLine returns the line that ends at the current position and
// moves back to the end of the text of the line before it (before its line
// break).
func (p *parser) readPreviousLine() ([]byte, error) {
	if p.pos == 0 {
		return nil, classErr("PdfStreamError", "Stream has ended unexpectedly")
	}
	isCRLF := func(c byte) bool { return c == '\r' || c == '\n' }
	i := p.pos - 1
	for i >= 0 && !isCRLF(p.data[i]) {
		i--
	}
	line := p.data[i+1 : p.pos]
	if i < 0 {
		p.pos = 0
		return line, nil
	}
	for i >= 0 && isCRLF(p.data[i]) {
		i--
	}
	p.pos = i + 1
	return line, nil
}

func (d *Document) read() error {
	if len(d.data) == 0 {
		return classErr("EmptyFileError", "Cannot read an empty file")
	}
	p := newParser(d, len(d.data))
	// find the %%EOF marker, reading lines backwards from the end
	var line []byte
	first := true
	for !hasPrefix(line, "%%EOF") {
		if len(line) != 0 && first {
			s := trimSpaceBytes(line)
			trunc := false
			for _, tr := range []string{"%%EO", "%%E", "%%", "%"} {
				if bytes.HasSuffix(s, []byte(tr)) {
					trunc = true
				}
			}
			if trunc {
				break
			}
			first = false
		}
		var err error
		line, err = p.readPreviousLine()
		if err != nil {
			return err
		}
	}
	startxref, err := d.findStartxrefPos(p)
	if err != nil {
		return err
	}
	issue := d.getXrefIssues(startxref)
	if err := d.readXrefTablesAndTrailers(p, startxref, issue); err != nil {
		return err
	}
	if d.xrefIndex != 0 || d.xrefIndexBad {
		// This iterates the table it started with even if a rebuild
		// replaces d.xref; writes then go to the new table.
		old, order := d.xref, append([]int(nil), d.xrefOrder...)
		for _, gen := range order {
			entries := old[gen]
			if gen == 65535 {
				continue
			}
			for _, id := range sortedKeys(entries) {
				off, ok := entries[id]
				if !ok {
					return classErr("KeyError", "%d", id)
				}
				if off < 0 {
					return classErr("ValueError", "negative seek value %d", off)
				}
				q := newParser(d, off)
				pid, _, err := d.readObjectHeader(q)
				if err != nil {
					if errClass(err) == "ValueError" {
						if err := d.rebuildXrefTable(); err != nil {
							return err
						}
						break
					}
					return err
				}
				if d.xrefIndexBad {
					return classErr("TypeError", "unsupported operand type(s) for -")
				}
				if pid == id-d.xrefIndex {
					cur, ok := d.xref[gen]
					if !ok {
						return classErr("KeyError", "%d", gen)
					}
					v, ok := cur[id]
					if !ok {
						return classErr("KeyError", "%d", id)
					}
					cur[pid] = v
					delete(cur, id)
				}
			}
		}
	}
	for _, gen := range append([]int(nil), d.xrefOrder...) { // insertion order
		entries, ok := d.xref[gen]
		if !ok {
			continue
		}
		if gen == 65535 {
			continue
		}
		for _, id := range sortedKeys(entries) {
			if entries[id] < 0 {
				return classErr("ValueError", "negative seek value %d", entries[id])
			}
			q := newParser(d, entries[id])
			if _, _, err := d.readObjectHeader(q); err != nil {
				if errClass(err) == "ValueError" {
					delete(entries, id)
					continue
				}
				return err
			}
		}
	}
	return nil
}

func sortedKeys(m map[int]int) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (d *Document) findStartxrefPos(p *parser) (int, error) {
	line, err := p.readPreviousLine()
	if err != nil {
		return 0, err
	}
	v, err := parseIntLoose(line)
	if err != nil {
		if !hasPrefix(line, "startxref") {
			return 0, classErr("PdfReadError", "startxref not found")
		}
		v, err = parseIntLoose(line[9:])
		if err != nil {
			return 0, err
		}
		return int(v), nil
	}
	line, err = p.readPreviousLine()
	if err != nil {
		return 0, err
	}
	if !hasPrefix(line, "startxref") {
		// look further back for a startxref line
		offset := int64(-1)
		haveOffset := false
		for i := 0; i < 1000; i++ {
			if p.pos <= 0 {
				break
			}
			l, err := p.readPreviousLine()
			if err != nil {
				return 0, err
			}
			if !hasPrefix(l, "startxref") {
				if o, err := parseIntLoose(l); err == nil {
					offset, haveOffset = o, true
				} else {
					haveOffset = false
				}
				continue
			}
			if len(l) > 9 {
				o, err := parseIntLoose(l[9:])
				if err != nil {
					return 0, err
				}
				return int(o), nil
			}
			if haveOffset {
				return int(offset), nil
			}
			break
		}
		return 0, classErr("PdfReadError", "startxref not found")
	}
	return int(v), nil
}

func (d *Document) getXrefIssues(startxref int) int {
	if startxref == 0 {
		return 4
	}
	p := newParser(d, startxref-1)
	if p.pos < 0 {
		p.pos = 0
	}
	c := p.read1()
	if c == 'j' {
		c = p.read1()
	}
	if c >= 0 && !(c == '\r' || c == '\n' || c == ' ' || c == '\t') {
		return 1
	}
	if string(p.read(4)) != "xref" {
		c := -2 // nothing read yet
		for c == -2 || (c >= '0' && c <= '9') || c == ' ' || c == '\t' {
			c = p.read1()
			if c < 0 {
				return 2
			}
		}
		l := append([]byte{byte(c)}, p.read(2)...)
		if string(bytes.ToLower(l)) != "obj" {
			return 3
		}
	}
	return 0
}

func (d *Document) readXrefTablesAndTrailers(p *parser, startxref int, issue int) error {
	d.xref = map[int]map[int]int{}
	d.xrefOrder = nil
	d.xrefFree = map[int]map[int]bool{}
	d.xrefObjStm = map[int][2]int{}
	d.trailer = newDict()
	visited := map[int]bool{}
	cur := startxref
	have := true
	for have {
		if visited[cur] {
			break
		}
		visited[cur] = true
		if cur < 0 {
			return classErr("ValueError", "negative seek value")
		}
		p.pos = minInt(cur, len(d.data))
		x := p.read1()
		if x == '\r' || x == '\n' || x < 0 {
			x = p.read1()
		}
		switch {
		case x == 'x':
			next, ok, err := d.readXref(p)
			if err != nil {
				return err
			}
			cur, have = next, ok
		case issue != 0:
			if err := catch(d.rebuildXrefTable); err == nil {
				have = false
			} else {
				issue = 0
			}
		case x >= '0' && x <= '9':
			var xs *Stream
			err := catch(func() error {
				var e error
				xs, e = d.readXrefStream(p)
				return e
			})
			if err != nil {
				if d.trailer.Has("/Root") {
					have = false
					continue
				}
				return classErr("PdfReadError", "Trailer cannot be read: %v", err)
			}
			if err := d.processXrefStream(xs); err != nil {
				return err
			}
			if prev, ok := xs.Dict.Get("/Prev"); ok {
				pv, isInt := d.resolve(prev).(Int)
				if !isInt {
					return classErr("TypeError", "/Prev")
				}
				cur = int(pv)
			} else {
				have = false
			}
		default:
			next, ok, err := d.readXrefOtherError(p, cur)
			if err != nil {
				return err
			}
			cur, have = next, ok
		}
	}
	return nil
}

func (d *Document) processXrefStream(xs *Stream) error {
	for _, k := range []Name{"/Root", "/Encrypt", "/Info", "/ID", "/Size"} {
		if v, ok := xs.Dict.Get(k); ok && !d.trailer.Has(k) {
			d.trailer.Set(k, v)
		}
	}
	if v, ok := xs.Dict.Get("/XRefStm"); ok {
		n, isInt := d.resolve(v).(Int)
		if !isInt {
			return classErr("TypeError", "/XRefStm")
		}
		q := newParser(d, int(n)+1)
		if _, err := d.readXrefStream(q); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) readXrefOtherError(p *parser, startxref int) (int, bool, error) {
	if startxref == 0 {
		return 0, false, nil
	}
	p.back(11)
	tmp := p.read(20)
	if loc := bytes.Index(tmp, []byte("xref")); loc != -1 {
		return startxref - (10 - loc), true, nil
	}
	p.pos = startxref
	for look := 0; look < 25; look++ {
		c := p.read1()
		if c >= '0' && c <= '9' {
			return startxref + look, true, nil
		}
	}
	if d.trailer.Has("/Root") {
		if err := catch(d.rebuildXrefTable); err != nil {
			return 0, false, classErr("PdfReadError", "Cannot rebuild xref")
		}
		return 0, false, nil
	}
	return 0, false, classErr("PdfReadError", "Could not find xref table at specified location")
}

func (d *Document) readXref(p *parser) (int, bool, error) {
	done, err := d.readStandardXrefTable(p)
	if err != nil {
		return 0, false, err
	}
	if done {
		// rebuilt; the stream was read to its end
		return 0, false, nil
	}
	if p.pos >= len(d.data) {
		return 0, false, nil
	}
	p.readNonWhitespace()
	p.back(1)
	o, err := p.readObject()
	if err != nil {
		return 0, false, err
	}
	nt, ok := o.(*Dict)
	if !ok {
		if s, isS := o.(*Stream); isS {
			nt = s.Dict
		} else {
			return 0, false, classErr("AttributeError", "trailer is not a dictionary")
		}
	}
	for _, k := range nt.Keys() {
		if !d.trailer.Has(k) {
			d.trailer.Set(k, nt.m[k])
		}
	}
	if v, ok := nt.Get("/XRefStm"); ok {
		if n, isInt := d.resolve(v).(Int); isInt {
			q := newParser(d, int(n)+1)
			_ = catch(func() error { _, e := d.readXrefStream(q); return e })
		}
	}
	if v, ok := nt.Get("/Prev"); ok {
		n, isInt := d.resolve(v).(Int)
		if !isInt {
			return 0, false, classErr("TypeError", "/Prev")
		}
		return int(n), true, nil
	}
	return 0, false, nil
}

// readStandardXrefTable reads a classic xref table (after "x"); done
// reports that the table was rebuilt instead.
func (d *Document) readStandardXrefTable(p *parser) (bool, error) {
	if string(p.read(3)) != "ref" {
		return false, classErr("PdfReadError", "xref table read error")
	}
	p.readNonWhitespace()
	p.back(1)
	firstTime := true
	var recovery map[int][2]int
	for {
		numObj, err := p.readObject()
		if err != nil {
			return false, err
		}
		num, isInt := numObj.(Int)
		if r, isReal := numObj.(Real); isReal && float64(r) == float64(int64(r)) {
			// a whole real number is the same key as the integer
			num, isInt = Int(int64(r)), true
		}
		if firstTime && !(isInt && num == 0) {
			if isInt {
				d.xrefIndex = int(num)
			} else {
				d.xrefIndexBad = true // a non-number: arithmetic on it raises later
			}
		}
		firstTime = false
		p.readNonWhitespace()
		p.back(1)
		sizeObj, err := p.readObject()
		if err != nil {
			return false, err
		}
		size, isInt2 := sizeObj.(Int)
		if !isInt2 {
			if err := d.rebuildXrefTable(); err != nil {
				return false, err
			}
			p.pos = len(d.data)
			return true, nil
		}
		if !isInt && size > 0 {
			return false, classErr("TypeError", "xref num")
		}
		p.readNonWhitespace()
		p.back(1)
		n := int(num)
		for cnt := 0; cnt < int(size); cnt++ {
			line := p.read(20)
			if len(line) == 0 {
				return false, classErr("PdfReadError", "Unexpected empty line in Xref table.")
			}
			for len(line) > 0 && (line[0] == '\r' || line[0] == '\n') {
				before := p.pos
				p.back(19)
				line = p.read(20)
				if p.pos <= before {
					// Near the end of the file this realignment can stop
					// moving; fail rather than loop forever.
					return false, classErr("PdfReadError", "xref table entry realignment makes no progress")
				}
			}
			if len(line) > 0 {
				last := line[len(line)-1]
				if (last >= '0' && last <= '9') || last == 't' {
					p.back(1)
				}
			}
			var offset, generation int
			var typ string
			parsed := false
			head := line
			if len(head) > 16 {
				head = head[:16]
			}
			parts := bytes.Split(head, []byte(" "))
			if len(parts) == 2 {
				o, e1 := parseIntLoose(parts[0])
				g, e2 := parseIntLoose(parts[1])
				if e1 == nil && e2 == nil {
					offset, generation, parsed = int(o), int(g), true
					if len(line) >= 18 {
						typ = string(line[17:18])
					}
				}
			}
			if !parsed {
				if recovery == nil {
					recovery = loadRecoveryCache(d.data)
				}
				if rc, ok := recovery[n]; ok {
					// a recovered (object start, generation) pair is used
					// as (generation, offset); the output depends on the swap.
					generation, offset, typ = rc[0], rc[1], "n"
				} else {
					generation, offset, typ = 65535, -1, "f"
				}
			}
			if d.xref[generation] == nil {
				d.ensureGen(generation)
				d.xrefFree[generation] = map[int]bool{}
			}
			if _, exists := d.xref[generation][n]; !exists {
				if typ == "n" {
					d.xref[generation][n] = offset
				}
				if f, ok := d.xrefFree[generation]; ok {
					f[n] = typ == "f"
				}
				if f, ok := d.xrefFree[65535]; ok {
					f[n] = typ == "f"
				}
			}
			n++
		}
		p.readNonWhitespace()
		p.back(1)
		for {
			c := p.read1()
			if c != '%' {
				break
			}
			p.back(1)
			if err := p.skipOverComment(); err != nil {
				return false, err
			}
			p.readNonWhitespace()
			p.back(1)
		}
		// step back over the loop's last read, then read 7 bytes
		p.back(1)
		tag := p.read(7)
		if string(tag) != "trailer" {
			p.back(7)
		} else {
			break
		}
	}
	return false, nil
}

// findPDFObjects calls fn for every "<num> <gen> obj" in the data, with the
// position where num starts.
func findPDFObjects(data []byte, fn func(num, gen, start int)) {
	idx := 0
	for {
		i := bytes.Index(data[idx:], []byte(" obj"))
		if i < 0 {
			return
		}
		index := idx + i
		j := index - 1
		for j >= 0 && isPdfWhite(data[j]) {
			j--
		}
		genEnd := j + 1
		for j >= 0 && data[j] >= '0' && data[j] <= '9' {
			j--
		}
		genStart := j + 1
		for j >= 0 && isPdfWhite(data[j]) {
			j--
		}
		objEnd := j + 1
		for j >= 0 && data[j] >= '0' && data[j] <= '9' {
			j--
		}
		objStart := j + 1
		if objStart < objEnd && genStart < genEnd {
			on, e1 := strconv.Atoi(string(data[objStart:objEnd]))
			gn, e2 := strconv.Atoi(string(data[genStart:genEnd]))
			if e1 == nil && e2 == nil {
				fn(on, gn, objStart)
			}
		}
		idx = index + 4
	}
}

func loadRecoveryCache(data []byte) map[int][2]int {
	cache := map[int][2]int{}
	findPDFObjects(data, func(num, gen, start int) {
		if _, ok := cache[num]; !ok {
			cache[num] = [2]int{start, gen}
		}
	})
	return cache
}

// rebuildXrefTable rebuilds the xref from the "obj" headers in the file,
// adds the objects inside object streams, and reads every trailer.
func (d *Document) rebuildXrefTable() error {
	d.xref = map[int]map[int]int{}
	d.xrefOrder = nil
	keyOrder := map[int][]int{} // per generation, first-insertion order
	findPDFObjects(d.data, func(num, gen, start int) {
		m := d.ensureGen(gen)
		if _, seen := m[num]; !seen {
			keyOrder[gen] = append(keyOrder[gen], num)
		}
		m[num] = start
	})
	for _, gen := range d.xrefOrder {
		entries := d.xref[gen]
		for _, num := range keyOrder[gen] {
			_ = catch(func() error {
				q := newParser(d, entries[num])
				if _, _, err := d.readObjectHeader(q); err != nil {
					return err
				}
				o, err := q.readObject()
				if err != nil {
					return err
				}
				st, ok := o.(*Stream)
				if !ok {
					return nil
				}
				if t, _ := st.Dict.Get("/Type"); t != Name("/ObjStm") {
					return nil
				}
				data, err := d.streamData(st)
				if err != nil {
					return err
				}
				sp := &parser{data: data}
				for {
					cur := sp.readUntilWhitespace()
					if !isDigits(cur) {
						break
					}
					inner, _ := strconv.Atoi(string(cur))
					sp.skipOverWhitespace()
					sp.back(1)
					cur = sp.readUntilWhitespace()
					if !isDigits(cur) {
						break
					}
					ig, _ := strconv.Atoi(string(cur))
					d.xrefObjStm[inner] = [2]int{num, ig}
				}
				return nil
			})
		}
	}
	// trailers, from start to end; later ones overwrite
	idx := 0
	for {
		i := bytes.Index(d.data[idx:], []byte("trailer"))
		if i < 0 {
			break
		}
		at := idx + i + 7
		for at < len(d.data) && isPdfWhite(d.data[at]) {
			at++
		}
		if at+1 < len(d.data) && d.data[at] == '<' && d.data[at+1] == '<' {
			q := newParser(d, at)
			o, err := q.readObject()
			if err != nil {
				return err
			}
			if nt, ok := asDict(o); ok {
				for _, k := range nt.Keys() {
					d.trailer.Set(k, nt.m[k])
				}
			}
		}
		idx += i + 7
	}
	return nil
}

func isDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// readXrefStream reads a cross-reference stream; p is positioned one byte
// after the start of the object (it steps back one).
func (d *Document) readXrefStream(p *parser) (*Stream, error) {
	p.back(1)
	num, gen, err := d.readObjectHeader(p)
	if err != nil {
		return nil, err
	}
	o, err := p.readObject()
	if err != nil {
		return nil, err
	}
	xs, ok := o.(*Stream)
	if !ok {
		return nil, classErr("TypeError", "xref stream is not a stream")
	}
	if t, _ := xs.Dict.Get("/Type"); d.resolve(t) != Name("/XRef") {
		return nil, classErr("PdfReadError", "Unexpected type %v", t)
	}
	d.cache[[2]int{gen, num}] = xs
	if !xs.Dict.Has("/Size") {
		return nil, classErr("PdfReadError", "Size missing from XRef stream")
	}
	var index []int
	if io, ok := xs.Dict.Get("/Index"); ok {
		arr, _ := d.resolve(io).(Array)
		for _, x := range arr {
			v, _, ok := d.num(x)
			if !ok {
				return nil, classErr("TypeError", "/Index")
			}
			index = append(index, int(v))
		}
	} else {
		sz, _, _ := d.num(xs.Dict.m["/Size"])
		index = []int{0, int(sz)}
	}
	wArr, _ := d.resolve(xs.Dict.m["/W"]).(Array)
	if len(wArr) < 3 {
		return nil, classErr("AssertionError", "W")
	}
	var w []int
	for _, x := range wArr {
		v, _, _ := d.num(x)
		w = append(w, int(v))
	}
	data, err := d.streamData(xs)
	if err != nil {
		return nil, err
	}
	minEntry := w[0] + w[1] + w[2]
	if minEntry == 0 {
		index = nil
	} else {
		maxEntries := len(data)/minEntry + 1
		total := 0
		for i := range index {
			if i%2 == 1 {
				if total+index[i] > maxEntries {
					v := maxEntries - total
					if v < 0 {
						v = 0
					}
					index[i] = v
				}
				total += index[i]
			}
		}
	}
	pos := 0
	getEntry := func(i int) (int, error) {
		if w[i] > 0 {
			if w[i] > 8 {
				return 0, classErr("PdfReadError", "Invalid size in convert_to_int")
			}
			e := pos + w[i]
			if e > len(data) {
				e = len(data)
			}
			chunk := data[pos:e]
			pos = e
			v := 0
			for _, c := range chunk {
				v = v<<8 | int(c)
			}
			return v, nil
		}
		if i == 0 {
			return 1, nil
		}
		return 0, nil
	}
	usedBefore := func(num, gen int) bool {
		if m, ok := d.xref[gen]; ok {
			if _, ok := m[num]; ok {
				return true
			}
		}
		_, ok := d.xrefObjStm[num]
		return ok
	}
	for i := 0; i+1 < len(index); i += 2 {
		start, size := index[i], index[i+1]
		for num := start; num < start+size; num++ {
			typ, err := getEntry(0)
			if err != nil {
				return nil, err
			}
			switch typ {
			case 0:
				if _, err := getEntry(1); err != nil {
					return nil, err
				}
				if _, err := getEntry(2); err != nil {
					return nil, err
				}
			case 1:
				off, err := getEntry(1)
				if err != nil {
					return nil, err
				}
				g, err := getEntry(2)
				if err != nil {
					return nil, err
				}
				d.ensureGen(g)
				if !usedBefore(num, g) {
					d.xref[g][num] = off
				}
			case 2:
				stm, err := getEntry(1)
				if err != nil {
					return nil, err
				}
				idx, err := getEntry(2)
				if err != nil {
					return nil, err
				}
				if !usedBefore(num, 0) {
					d.xrefObjStm[num] = [2]int{stm, idx}
				}
			}
		}
	}
	return xs, nil
}

// readObjectHeader reads "<num> <gen> obj" and returns num and gen.
func (d *Document) readObjectHeader(p *parser) (int, int, error) {
	if err := p.skipOverComment(); err != nil {
		return 0, 0, err
	}
	p.skipOverWhitespace()
	p.back(1)
	idnum := p.readUntilWhitespace()
	p.skipOverWhitespace()
	p.back(1)
	gen := p.readUntilWhitespace()
	p.skipOverWhitespace()
	p.back(1)
	p.read(3)
	p.readNonWhitespace()
	p.back(1)
	n, err := parseIntLoose(idnum)
	if err != nil {
		return 0, 0, err
	}
	g, err := parseIntLoose(gen)
	if err != nil {
		return 0, 0, err
	}
	return int(n), int(g), nil
}

// searchObject finds the first "\s<num>\s+<gen>\s+obj" in the file. The
// file never changes, so results are cached per object.
func (d *Document) searchObject(num, gen int) []int {
	key := [2]int{num, gen}
	if m, ok := d.searchCache[key]; ok {
		return m
	}
	re := regexp.MustCompile(fmt.Sprintf(`[ \t\n\r\f\v]%d[ \t\n\r\f\v]+%d[ \t\n\r\f\v]+obj`, num, gen))
	m := re.FindIndex(d.data)
	if d.searchCache == nil {
		d.searchCache = map[[2]int][]int{}
	}
	d.searchCache[key] = m
	return m
}

// getObject reads the object r refers to (from the cache, an object stream,
// the xref offset, or a search of the file).
func (d *Document) getObject(r Ref) (Object, error) {
	key := [2]int{r.Gen, r.Num}
	if v, ok := d.cache[key]; ok && v != nil {
		return v, nil
	}
	// resolving a reference goes through three modelled frames
	if d.frame+3 > recursionLimit {
		return nil, recursionError()
	}
	var retval Object
	if _, inStm := d.xrefObjStm[r.Num]; r.Gen == 0 && inStm {
		return d.getObjectFromStream(r)
	}
	if start, ok := d.xref[r.Gen][r.Num]; ok {
		if d.xrefFree[r.Gen][r.Num] {
			return Null{}, nil
		}
		if start < 0 {
			return nil, classErr("ValueError", "negative seek value %d", start)
		}
		p := newParser(d, start)
		idnum, generation, err := d.readObjectHeader(p)
		if err != nil || idnum != r.Num || generation != r.Gen {
			if m := d.searchObject(r.Num, r.Gen); m != nil {
				d.xref[r.Gen][r.Num] = m[0] + 1
				p.pos = m[0] + 1
				if _, _, err := d.readObjectHeader(p); err != nil {
					return nil, err
				}
			}
		}
		if d.known[key] {
			return nil, classErr("LimitReachedError", "Detected loop with self reference")
		}
		d.known[key] = true
		o, err := p.readObject()
		if err != nil {
			// No cleanup here: the marker stays, and the next access
			// reports a self-reference loop.
			return nil, err
		}
		delete(d.known, key)
		retval = o
	} else {
		if m := d.searchObject(r.Num, r.Gen); m != nil {
			d.ensureGen(r.Gen)[r.Num] = m[0] + 1
			p := newParser(d, m[1]+1)
			p.skipOverWhitespace()
			p.back(1)
			o, err := p.readObject()
			if err != nil {
				return nil, err
			}
			retval = o
		}
	}
	d.cache[key] = retval
	if err := refBodyError(retval); err != nil {
		return nil, err
	}
	return retval, nil
}

// refBodyError: an object whose body is just a reference is stored in the
// cache, but its first access raises AttributeError (later ones return the
// reference itself).
func refBodyError(o Object) error {
	if _, isRef := o.(Ref); isRef {
		return classErr("AttributeError", "property 'indirect_reference' of 'IndirectObject' object has no setter")
	}
	return nil
}

func (d *Document) getObjectFromStream(r Ref) (Object, error) {
	stmnum := d.xrefObjStm[r.Num][0]
	// reading from an object stream runs at frame+3 and resolves the stream
	// object from there
	base := d.frame
	d.frame = base + 3
	so, err := d.getObject(Ref{Num: stmnum})
	d.frame = base
	if err != nil {
		return nil, err
	}
	sd, ok := asDict(so)
	if !ok {
		return nil, classErr("TypeError", "object stream is not subscriptable")
	}
	t, ok := sd.Get("/Type")
	if !ok {
		return nil, classErr("KeyError", "/Type")
	}
	if d.resolve(t) != Name("/ObjStm") {
		return nil, classErr("AssertionError", "not an /ObjStm")
	}
	st, ok := so.(*Stream)
	if !ok {
		return nil, classErr("AttributeError", "no get_data")
	}
	data, err := d.streamData(st)
	if err != nil {
		return nil, err
	}
	nv, _, ok1 := d.num(st.Dict.m["/N"])
	fv, _, ok2 := d.num(st.Dict.m["/First"])
	if !ok1 || !ok2 {
		return nil, classErr("TypeError", "/N or /First")
	}
	n, first := int(nv), int(fv)
	if maxN := len(data) / 3; n > maxN {
		n = maxN
	}
	sp := &parser{data: data, doc: d, frame: base + 4}
	type entry struct{ num, off int }
	var index []entry
	for i := 0; i < n; i++ {
		sp.readNonWhitespace()
		sp.back(1)
		on, err := sp.readNumber()
		if err != nil {
			return nil, err
		}
		sp.readNonWhitespace()
		sp.back(1)
		off, err := sp.readNumber()
		if err != nil {
			return nil, err
		}
		sp.readNonWhitespace()
		sp.back(1)
		a, _, _ := d.num(on)
		b, _, _ := d.num(off)
		index = append(index, entry{int(a), int(b)})
	}
	var target Object = Null{}
	for _, e := range index {
		if c, ok := d.cache[[2]int{0, e.num}]; ok && c != nil {
			if e.num == r.Num {
				target = c
			}
			continue
		}
		sp.pos = first + e.off
		if sp.pos < 0 {
			return nil, classErr("ValueError", "negative seek value")
		}
		sp.readNonWhitespace()
		sp.back(1)
		o, err := sp.readObject()
		if err != nil {
			if errClass(err) == "PdfStreamError" {
				o = Null{}
			} else {
				return nil, err
			}
		}
		if auth, ok := d.xrefObjStm[e.num]; ok && auth[0] == stmnum {
			d.cache[[2]int{0, e.num}] = o
			if err := refBodyError(o); err != nil {
				return nil, err
			}
		}
		if e.num == r.Num {
			target = o
		}
	}
	return target, nil
}

// rootObject returns the document catalog: the trailer's /Root when it is a
// /Catalog, otherwise the first /Catalog among objects 1 to /Size,
// otherwise a /Root that has /Pages.
func (d *Document) rootObject() *Dict {
	if d.root != nil {
		return d.root
	}
	root, _ := d.trailer.Get("/Root")
	var rootDict *Dict
	if !isNullOrNone(d, root) {
		rootDict, _ = d.resolve(root).(*Dict)
		if rootDict == nil {
			if s, ok := d.resolve(root).(*Stream); ok {
				rootDict = s.Dict
			}
		}
		if rootDict != nil {
			if t, _ := rootDict.Get("/Type"); t == Name("/Catalog") {
				d.root = rootDict
			}
		} else {
			raise(classErr("AttributeError", "root has no get"))
		}
	}
	if d.root == nil {
		size := 0
		if s, ok := d.trailer.Get("/Size"); ok {
			if v, isInt := s.(Int); isInt {
				size = int(v)
			}
		}
		for i := 0; i < size; i++ {
			if i >= 10000 { // the /Root recovery limit
				raise(classErr("LimitReachedError", "Maximum Root object recovery limit reached."))
			}
			var obj Object
			_ = catch(func() error {
				var e error
				obj, e = d.getObject(Ref{Num: i + 1})
				return e
			})
			if od, ok := asDict(obj); ok {
				if t, _ := od.Get("/Type"); t == Name("/Catalog") {
					d.root = od
					break
				}
			}
		}
	}
	if d.root == nil {
		if rootDict != nil && rootDict.Has("/Pages") {
			d.root = rootDict
		} else {
			raise(classErr("PdfReadError", "Cannot find Root object in pdf"))
		}
	}
	return d.root
}

// pages walks the page tree: copies of the page dictionaries, with
// inherited attributes filled in.
func (d *Document) pages() []*Dict {
	saved := d.frame
	defer func() { d.frame = saved }()
	d.frame = frameFlattenRoot
	catalog := d.rootObject()
	po, ok := catalog.Get("/Pages")
	if !ok {
		raise(classErr("AttributeError", "'NoneType' object has no attribute 'get_object'"))
	}
	pagesObj := d.resolve(po)
	pages, ok := asDict(pagesObj)
	if !ok {
		raise(classErr("PdfReadError", "Invalid object in /Pages"))
	}
	var out []*Dict
	inherit := newDict()
	visited := map[*Dict]bool{}
	var walk func(node *Dict, self Object, level int)
	walk = func(node *Dict, self Object, level int) {
		// the walk recurses once per tree level
		if frameFlattenRoot+level > recursionLimit {
			raise(recursionError())
		}
		d.frame = frameFlattenRoot + level
		var t Object
		if node.Has("/Type") {
			t = d.resolve(node.m["/Type"])
		} else if !node.Has("/Kids") {
			t = Name("/Page")
		} else {
			t = Name("/Pages")
		}
		switch t {
		case Name("/Pages"):
			for _, a := range []Name{"/Resources", "/MediaBox", "/CropBox", "/Rotate"} {
				if node.Has(a) {
					inherit.Set(a, d.resolve(node.m[a]))
				}
			}
			var kids Array
			if k, ok := node.Get("/Kids"); ok {
				switch kv := d.resolve(k).(type) {
				case Array:
					kids = kv
				case Null:
				default:
					raise(classErr("PdfReadError", "Expected /Kids to be an array"))
				}
			}
			for _, kid := range kids {
				if kr, isRef := kid.(Ref); isRef {
					if sr, ok := self.(Ref); ok && sr == kr {
						raise(classErr("PdfReadError", "Detected cyclic page references."))
					}
				}
				obj := d.resolve(kid)
				if !truthy(obj) {
					continue
				}
				kd, ok := asDict(obj)
				if !ok {
					raise(classErr("TypeError", "page tree node is not a dictionary"))
				}
				if visited[kd] {
					raise(classErr("PdfReadError", "Detected cyclic page references."))
				}
				visited[kd] = true
				walk(kd, kid, level+1)
				d.frame = frameFlattenRoot + level
			}
		case Name("/Page"):
			for _, a := range inherit.Keys() {
				if !node.Has(a) {
					if inherit.m[a] == nil {
						raise(classErr("ValueError", "Value must be a PdfObject"))
					}
					node.Set(a, inherit.m[a])
				}
			}
			out = append(out, node.copyDict())
		}
	}
	walk(pages, po, 0)
	return out
}

func asDict(o Object) (*Dict, bool) {
	switch v := o.(type) {
	case *Dict:
		return v, true
	case *Stream:
		return v.Dict, true
	}
	return nil, false
}

// ensureGen returns xref[gen], creating it (and recording the order).
func (d *Document) ensureGen(gen int) map[int]int {
	m, ok := d.xref[gen]
	if !ok {
		m = map[int]int{}
		d.xref[gen] = m
		d.xrefOrder = append(d.xrefOrder, gen)
	}
	return m
}
