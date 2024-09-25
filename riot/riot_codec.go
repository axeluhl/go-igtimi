package riot

import (
	"encoding/json"
	"io"

	"github.com/igtimi/go-igtimi/gen/go/com/igtimi"
	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/encoding/protojson"
)

type CodecType int

const (
	CodecTypeProto CodecType = iota
	CodecTypeJson
)

type Codec interface {
	Encode(w io.Writer, msg *igtimi.Msg) error
	Decode(r Reader, msg *igtimi.Msg) error
}

type Reader interface {
	io.Reader
	io.ByteReader
}

type CodecProto struct{}

func (c *CodecProto) Encode(w io.Writer, msg *igtimi.Msg) error {
	_, err := protodelim.MarshalTo(w, msg)
	return err
}

func (c *CodecProto) Decode(r Reader, msg *igtimi.Msg) error {
	return protodelim.UnmarshalFrom(r, msg)
}

type CodecJson struct{}

func (c *CodecJson) Encode(w io.Writer, msg *igtimi.Msg) error {
	buf, err := protojson.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

func (c *CodecJson) Decode(r Reader, msg *igtimi.Msg) error {
	dec := json.NewDecoder(r)
	var raw json.RawMessage
	err := dec.Decode(&raw)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(raw, msg)
}
