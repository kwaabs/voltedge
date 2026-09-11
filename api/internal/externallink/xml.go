package externallink

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// xmlToMap decodes an XML document into a JSON-serializable nested value
// so a legacy XML system (response_format='xml') renders through the same
// panel data card as a JSON one. The mapping is deliberately simple and
// lossy-but-predictable:
//
//   - the document becomes {"<root>": <value>};
//   - an element with children becomes an object keyed by child name;
//   - repeated child elements collapse into an array under that name;
//   - attributes become "@name" keys;
//   - an element's own text, when it also has children/attributes, is
//     kept under "#text"; a leaf element with only text becomes that text
//     string directly.
//
// It is not a round-trippable XML model -- it is a readable projection
// for display, matching what a caller expects to eyeball in the card.
func xmlToMap(data []byte) (any, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, invalid("xml: document has no root element")
		}
		if err != nil {
			return nil, invalid("the response was not valid XML: %v", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			val, err := decodeElement(dec, start)
			if err != nil {
				return nil, invalid("the response was not valid XML: %v", err)
			}
			return map[string]any{start.Name.Local: val}, nil
		}
	}
}

// decodeElement consumes one element (whose StartElement was already
// read) up to and including its EndElement, returning its value.
func decodeElement(dec *xml.Decoder, start xml.StartElement) (any, error) {
	node := map[string]any{}
	for _, a := range start.Attr {
		node["@"+a.Name.Local] = a.Value
	}
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeElement(dec, t)
			if err != nil {
				return nil, err
			}
			addChild(node, t.Name.Local, child)
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			trimmed := strings.TrimSpace(text.String())
			// A leaf with no attributes/children is just its text (or "").
			if len(node) == 0 {
				return trimmed, nil
			}
			if trimmed != "" {
				node["#text"] = trimmed
			}
			return node, nil
		}
	}
}

// addChild inserts a child under key, promoting to a slice when a second
// element of the same name appears (so <items><item/><item/></items>
// becomes {"item": [ ... ]}).
func addChild(node map[string]any, key string, val any) {
	existing, ok := node[key]
	if !ok {
		node[key] = val
		return
	}
	if arr, ok := existing.([]any); ok {
		node[key] = append(arr, val)
		return
	}
	node[key] = []any{existing, val}
}
