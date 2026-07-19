package api

import (
	"context"
	"net/url"
)

// Proto enum NAME strings — grpc-gateway serializes enums by name
// (MarshalOptions.UseProtoNames), so the JSON body must use these, not the
// lowercase DB spellings.
const (
	caseRefSourceImported = "CASE_REFERENCE_SOURCE_IMPORTED"
	caseRefKindTicket     = "CASE_REFERENCE_KIND_TICKET"
	caseRefKindLink       = "CASE_REFERENCE_KIND_LINK"
)

// CaseReferenceInput is one reference to attach to a case (OB-551). Kind is the
// semantic spelling ("ticket" | "link"); the client maps it to the proto name.
type CaseReferenceInput struct {
	Kind  string
	Value string
	Label string
	URL   string
}

type setReferencesBody struct {
	Source     string          `json:"source"`
	References []referenceBody `json:"references"`
}

type referenceBody struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
}

// SetTestCaseReferences replaces a case's IMPORTED references with refs, in one
// idempotent call (the server deletes the case's imported refs and inserts these;
// manually-added refs are untouched). caseIDOrCode is a UUID or a short compound
// code (e.g. DEMO-12).
func (c *Client) SetTestCaseReferences(ctx context.Context, caseIDOrCode string, refs []CaseReferenceInput) error {
	body := setReferencesBody{Source: caseRefSourceImported}
	for _, r := range refs {
		body.References = append(body.References, referenceBody{
			Kind:  protoRefKind(r.Kind),
			Value: r.Value,
			Label: r.Label,
			URL:   r.URL,
		})
	}
	path := "/api/cases/" + url.PathEscape(caseIDOrCode) + "/references"
	return c.DoJSON(ctx, "PUT", path, body, nil)
}

// protoRefKind maps a semantic kind to its proto enum name. An unknown kind maps
// to "" so the server rejects it rather than silently mis-storing.
func protoRefKind(k string) string {
	switch k {
	case "ticket":
		return caseRefKindTicket
	case "link":
		return caseRefKindLink
	default:
		return ""
	}
}
