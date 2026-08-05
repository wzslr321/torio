package platform

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestDecodeEnvelope(t *testing.T) {
	g := NewWithT(t)
	valid := []byte(`{"schema_version":"1","ok":true,"command":"vm.status","data":{"state":"running"},"warnings":[],"error":null}`)

	got, err := decodeEnvelope(valid, "vm.status")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got.Data).To(HaveKeyWithValue("state", "running"))

	invalid := map[string][]byte{
		"trailing document":      append(append([]byte{}, valid...), []byte("\n{}")...),
		"wrong command":          []byte(`{"schema_version":"1","ok":true,"command":"vm.start","data":{},"warnings":[],"error":null}`),
		"warning":                []byte(`{"schema_version":"1","ok":true,"command":"vm.status","data":{},"warnings":["drift"],"error":null}`),
		"unknown field":          []byte(`{"schema_version":"1","ok":true,"command":"vm.status","data":{},"warnings":[],"error":null,"extra":true}`),
		"duplicate envelope key": []byte(`{"schema_version":"1","ok":false,"ok":true,"command":"vm.status","data":{},"warnings":[],"error":null}`),
		"duplicate nested key":   []byte(`{"schema_version":"1","ok":true,"command":"vm.status","data":{"state":"stopped","state":"running"},"warnings":[],"error":null}`),
		"duplicate key in array": []byte(`{"schema_version":"1","ok":true,"command":"vm.status","data":{"items":[{"id":1,"id":2}]},"warnings":[],"error":null}`),
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := decodeEnvelope(body, "vm.status")
			g.Expect(err).To(HaveOccurred())
		})
	}
}

func TestNestedValue(t *testing.T) {
	g := NewWithT(t)
	value, found := nestedValue(map[string]any{"checkout": map[string]any{"repository": true}}, []string{"checkout", "repository"})
	g.Expect(found).To(BeTrue())
	g.Expect(value).To(Equal(true))
	_, found = nestedValue(map[string]any{}, []string{"missing"})
	g.Expect(found).To(BeFalse())
}
