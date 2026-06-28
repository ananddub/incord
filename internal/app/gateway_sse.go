package app

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

// sseMarshaler wraps the default JSONPb marshaler so each streamed message
// emerges framed as a Server-Sent Event: `data: <json>\n\n`. grpc-gateway
// calls Marshal once per server-stream send, and Delimiter between messages.
// We emit the delimiter inside Marshal to keep every event self-contained.
type sseMarshaler struct {
	runtime.JSONPb
}

func (m *sseMarshaler) ContentType(_ any) string { return "text/event-stream" }

func (m *sseMarshaler) Marshal(v any) ([]byte, error) {
	body, err := m.JSONPb.Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("data: ")
	buf.Write(body)
	buf.WriteString("\n\n")
	return buf.Bytes(), nil
}

func (m *sseMarshaler) Delimiter() []byte { return nil }

func (m *sseMarshaler) NewEncoder(w io.Writer) runtime.Encoder {
	return runtime.EncoderFunc(func(v any) error {
		b, err := m.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		if f, ok := w.(interface{ Flush() }); ok {
			f.Flush()
		}
		return err
	})
}

func (m *sseMarshaler) NewDecoder(r io.Reader) runtime.Decoder {
	return runtime.DecoderFunc(func(v any) error {
		return json.NewDecoder(r).Decode(v)
	})
}
