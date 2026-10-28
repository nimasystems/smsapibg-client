package smsapibg

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// The API documents numbers as JSON numbers, but a response that changed
// one to a string must not turn a message that was sent into an error (the
// caller might then send it again). These types accept both.

// looseFloat decodes a JSON number or a string holding one.
type looseFloat float64

func (f *looseFloat) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseFloat(string(unquote(b)), 64)
	if err != nil {
		return err
	}
	*f = looseFloat(v)
	return nil
}

// looseInt decodes a JSON integer or a string holding one.
type looseInt int64

func (n *looseInt) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(string(unquote(b)), 10, 64)
	if err != nil {
		return err
	}
	*n = looseInt(v)
	return nil
}

// looseString decodes a JSON string or a bare number (an ID sent unquoted).
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = looseString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = looseString(n.String())
	return nil
}

// unquote strips the quotes of a JSON string and maps null and "" to "0",
// so the numeric types read "1.5", 1.5, null and "" alike.
func unquote(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if string(b) == "null" || string(b) == `""` {
		return []byte("0")
	}
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return b[1 : len(b)-1]
	}
	return b
}

// The soft types are for optional fields: they never fail, and a value they
// cannot read becomes the zero value.

// softFloat decodes like looseFloat, or to 0.
type softFloat float64

func (f *softFloat) UnmarshalJSON(b []byte) error {
	var v looseFloat
	if v.UnmarshalJSON(b) == nil {
		*f = softFloat(v)
	}
	return nil
}

// softInt decodes an integer, or a number with a fraction (truncated), or
// a string holding either; anything else is 0.
type softInt int64

func (n *softInt) UnmarshalJSON(b []byte) error {
	var v looseFloat
	if v.UnmarshalJSON(b) == nil {
		*n = softInt(v)
	}
	return nil
}

// softString decodes like looseString, or to "".
type softString string

func (s *softString) UnmarshalJSON(b []byte) error {
	var v looseString
	if v.UnmarshalJSON(b) == nil {
		*s = softString(v)
	}
	return nil
}
