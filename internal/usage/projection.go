package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// A JSONL projection discards unneeded values while streaming, including huge
// content strings/arrays. Only metadata and usage ever enter the JSON decoder.
type projection struct {
	r        *bufio.Reader
	n        int64
	complete bool
	limit    int
	out      bytes.Buffer
}

var errProjection = errors.New("invalid or oversized metadata record")
var allowedKeys = map[string]bool{}

func init() {
	for _, k := range []string{"type", "uuid", "timestamp", "sessionId", "requestId", "cwd", "version", "entrypoint", "agentId", "isSidechain", "parentSessionId", "parentAgentId", "forkedFromId", "customTitle", "agentName", "message", "id", "model", "usage", "stop_reason", "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation", "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens", "output_tokens_details", "thinking_tokens", "iterations", "speed", "service_tier"} {
		allowedKeys[k] = true
	}
}

func (p *projection) read() (byte, error) {
	b, e := p.r.ReadByte()
	if e != nil {
		return 0, e
	}
	p.n++
	if b == '\n' {
		p.complete = true
		return 0, io.EOF
	}
	return b, nil
}
func (p *projection) peek() (byte, error) {
	b, e := p.r.Peek(1)
	if e != nil {
		return 0, e
	}
	if b[0] == '\n' {
		return 0, io.EOF
	}
	return b[0], nil
}
func (p *projection) spaces() error {
	for {
		b, e := p.peek()
		if e != nil {
			return e
		}
		if b != ' ' && b != '\t' && b != '\r' {
			return nil
		}
		_, _ = p.read()
	}
}
func (p *projection) put(b byte) error {
	if p.out.Len() >= p.limit {
		return errProjection
	}
	return p.out.WriteByte(b)
}
func (p *projection) str(keep bool) ([]byte, error) {
	var key []byte
	escape := false
	for {
		b, e := p.read()
		if e != nil {
			return nil, e
		}
		if keep {
			if len(key) > 4096 {
				return nil, errProjection
			}
			key = append(key, b)
		}
		if escape {
			escape = false
			continue
		}
		if b == '\\' {
			escape = true
			continue
		}
		if b == '"' {
			return key, nil
		}
		if b < 32 {
			return nil, errProjection
		}
	}
}
func (p *projection) skip(first byte) error {
	if first == '"' {
		_, e := p.str(false)
		return e
	}
	if first == '{' || first == '[' {
		depth := 1
		quoted, escaped := false, false
		for depth > 0 {
			b, e := p.read()
			if e != nil {
				return e
			}
			if quoted {
				if escaped {
					escaped = false
				} else if b == '\\' {
					escaped = true
				} else if b == '"' {
					quoted = false
				}
				continue
			}
			switch b {
			case '"':
				quoted = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		return nil
	}
	for {
		b, e := p.peek()
		if e != nil {
			return nil
		}
		if b == ',' || b == '}' || b == ']' || b == ' ' || b == '\r' || b == '\t' {
			return nil
		}
		_, _ = p.read()
	}
}
func (p *projection) value(keep bool, depth int) error {
	if depth > 64 {
		return errProjection
	}
	if e := p.spaces(); e != nil {
		return e
	}
	first, e := p.read()
	if e != nil {
		return e
	}
	if !keep {
		return p.skip(first)
	}
	if e = p.put(first); e != nil {
		return e
	}
	switch first {
	case '{':
		written := false
		for {
			if e = p.spaces(); e != nil {
				return e
			}
			b, _ := p.peek()
			if b == '}' {
				_, _ = p.read()
				return p.put('}')
			}
			if b != '"' {
				return errProjection
			}
			_, _ = p.read()
			raw, e := p.str(true)
			if e != nil {
				return e
			}
			var key string
			if json.Unmarshal(append([]byte{'"'}, raw...), &key) != nil {
				return errProjection
			}
			if e = p.spaces(); e != nil {
				return e
			}
			b, e = p.read()
			if e != nil || b != ':' {
				return errProjection
			}
			selected := allowedKeys[key]
			if selected {
				if written {
					if e = p.put(','); e != nil {
						return e
					}
				}
				written = true
				if e = p.put('"'); e != nil {
					return e
				}
				for _, c := range raw {
					if e = p.put(c); e != nil {
						return e
					}
				}
				if e = p.put(':'); e != nil {
					return e
				}
			}
			if e = p.value(selected, depth+1); e != nil {
				return e
			}
			if e = p.spaces(); e != nil {
				return e
			}
			b, e = p.read()
			if e != nil {
				return e
			}
			if b == '}' {
				return p.put('}')
			}
			if b != ',' {
				return errProjection
			}
		}
	case '[':
		for {
			if e = p.spaces(); e != nil {
				return e
			}
			b, _ := p.peek()
			if b == ']' {
				_, _ = p.read()
				return p.put(']')
			}
			if e = p.value(true, depth+1); e != nil {
				return e
			}
			if e = p.spaces(); e != nil {
				return e
			}
			b, e = p.read()
			if e != nil {
				return e
			}
			if b == ']' {
				return p.put(']')
			}
			if b != ',' {
				return errProjection
			}
			if e = p.put(','); e != nil {
				return e
			}
		}
	case '"':
		escaped := false
		for {
			b, e := p.read()
			if e != nil {
				return e
			}
			if e = p.put(b); e != nil {
				return e
			}
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				return nil
			}
		}
	default:
		for {
			b, e := p.peek()
			if e != nil {
				return nil
			}
			if b == ',' || b == '}' || b == ']' || b == ' ' || b == '\t' || b == '\r' {
				return nil
			}
			_, _ = p.read()
			if e = p.put(b); e != nil {
				return e
			}
		}
	}
}

func readProjectedRecord(r *bufio.Reader, limit int) ([]byte, int64, bool, error) {
	p := projection{r: r, limit: limit}
	err := p.value(true, 0)
	// Always resynchronize at the physical line boundary, but never consume an
	// unfinished tail in the persisted cursor.
	if !p.complete {
		for {
			b, e := p.read()
			if e != nil {
				break
			}
			if err == nil && b != ' ' && b != '\r' && b != '\t' {
				err = errProjection
			}
		}
	}
	if p.n == 0 {
		return nil, 0, false, io.EOF
	}
	if !p.complete {
		return nil, p.n, false, nil
	}
	return p.out.Bytes(), p.n, true, err
}
