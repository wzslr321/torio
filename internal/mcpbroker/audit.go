package mcpbroker

import "time"

// AuditRecord contains only the decision metadata ADR-0012 permits. It has no
// field capable of holding tool arguments, results, protocol bodies or tokens.
type AuditRecord struct {
	Time    time.Time `json:"time"`
	PeerUID uint32    `json:"peer_uid"`
	Service string    `json:"service"`
	Tool    string    `json:"tool"`
	Writes  bool      `json:"writes"`
	Allowed bool      `json:"allowed"`
}

func newAuditRecord(service, tool string, uid uint32, writes, allowed bool) AuditRecord {
	return AuditRecord{
		Time:    time.Now().UTC(),
		PeerUID: uid,
		Service: service,
		Tool:    tool,
		Writes:  writes,
		Allowed: allowed,
	}
}
