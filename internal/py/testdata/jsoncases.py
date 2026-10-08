import json, sys
cases = ['"\\u', '"\\u1', '"\\u123', '"\\ud800\\u12', '"\\ud800\\u', '{"a":1,}', '[1,]', '{"a" 1}', '{"a":1 "b"}', '[1 2]', '"abc', '"a\\x"', '"\\u12"', '"\\u12zz"', '"a\x01"', '', ' ', '{', '[', '{"a":', 'tru', '1 2', '-', '01', '1.', '1e', '{"a":1}\n{"b":2}', '﻿{}', '{"a":1,"a":2}', '-0', '1E400', '[1.5e-7, 1e16, 2.0, 1e22, 12345678901234567890, 0.1, 100.0, -0.0, 1e-5, 0.0001]', '{"é": "\\u00e9\\n\\u001b\\u007f\\u2028<>&"}', '[]', '{}', '{"a": [], "b": {}}', '\n\n  {"x" : 1 , }', '"\\ud83d\\ude00"', '[1,\n2,\n,3]']
out = []
for s in cases:
    try:
        v = json.loads(s)
        out.append([s, 'OK', json.dumps(v, ensure_ascii=False)])
    except json.JSONDecodeError as e:
        out.append([s, 'ERR', str(e)])
sys.stdout.buffer.write(json.dumps(out, ensure_ascii=True).encode())
