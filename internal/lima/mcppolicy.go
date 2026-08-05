package lima

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// The policy document is the grant. ADR-0004 §4 puts it at `root:root 0644` and
// says why in one line: readable by the agent, writable only by root. Both
// halves are load-bearing. Take away the read and the decision stops being
// legible to the party it constrains; take away the root-only write and it stops
// being a decision at all.
//
// Nothing else in the verification looks at these files, so a document the agent
// can rewrite would leave every other check green while granting itself whatever
// it liked. That is the failure this check exists for.
var (
	policyDocOwner = "root"
	policyDocGroup = "root"
	policyDocModes = map[string]bool{"644": true, "0644": true}
)

// policyFileSuffix is what the broker's own loader accepts. It is repeated here
// rather than imported so a guest-path rule and a host-side check cannot be made
// to disagree by an import cycle; the loader stays the source of truth and this
// check refuses anything it would refuse.
const policyFileSuffix = ".json"

// verifyPolicyDocuments proves the grant is a grant: root-owned, agent-readable,
// agent-unwritable, and a real file rather than a link to one.
func (a *Adapter) verifyPolicyDocuments(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "policy_documents"

	st, kind, err := a.statPath(ctx, rep, name, TorioMCPPolicyDir)
	if err != nil {
		return err
	}
	if st == pathUnprovable {
		return a.probeUnusable(rep, name, "the broker policy directory")
	}
	if st == pathAbsent {
		return a.brokerMissing(rep, name, "policy directory is absent",
			"run `torio mcp install` to provision the root-owned policy directory")
	}
	if kind != "directory" {
		return a.brokerFailed(rep, name, TorioMCPPolicyDir+" is not a directory",
			"inspect the guest by hand; the grant must live in a root-owned directory")
	}

	og, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPPolicyDir)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseStatOwnership(og.out)
	if og.exit != 0 || !ok {
		return a.brokerFailed(rep, name, "could not read policy directory ownership/mode",
			"verify "+TorioMCPPolicyDir+" on the guest")
	}
	writable, parsed := modeGrantsForeignWrite(mode)
	if !parsed {
		return a.brokerFailed(rep, name, "unparseable policy directory mode",
			"verify "+TorioMCPPolicyDir+" on the guest")
	}
	// A correct document inside a writable directory is a document that can be
	// replaced, so the directory's own mode is part of the grant.
	if owner != policyDocOwner || group != policyDocGroup || writable {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("policy directory is %s:%s %s", owner, group, mode),
			"the policy directory must be "+policyDocOwner+":"+policyDocGroup+" and writable by nobody else; reinstall rather than fixing it by hand")
	}

	listing, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "find", TorioMCPPolicyDir,
		"-mindepth", "1", "-maxdepth", "1", "-printf", `%f %u %g %m %y\n`)
	if err != nil {
		return err
	}
	if listing.exit != 0 {
		return a.brokerFailed(rep, name, "could not enumerate policy documents",
			"verify "+TorioMCPPolicyDir+" on the guest")
	}
	if strings.TrimSpace(listing.out) == "" {
		return a.brokerMissing(rep, name, "policy directory holds no service documents",
			"write /etc/torio-mcp/policy.d/<service>.json as root, then rerun `torio mcp install`")
	}

	documents := make(map[string][]byte)
	for _, line := range strings.Split(strings.TrimSpace(listing.out), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 {
			return a.brokerFailed(rep, name, "unparseable policy directory listing",
				"verify "+TorioMCPPolicyDir+" on the guest")
		}
		file, fOwner, fGroup, fMode, fType := f[0], f[1], f[2], f[3], f[4]

		// `%y` is the entry's own type, not its target's. A symlink here is
		// refused for the reason the broker's loader refuses it: the directory is
		// root-owned, but a link's target is not, so a link into the agent's home
		// hands the grant back to the identity it binds.
		if fType != "f" {
			return a.brokerFailed(rep, name,
				fmt.Sprintf("policy directory holds a non-regular entry of type %q", fType),
				"a policy document must be a regular file in this directory, never a link to one")
		}
		if !strings.HasSuffix(file, policyFileSuffix) {
			// The loader fails the whole set on a stray file rather than skipping
			// it, so a guest in this state has no working policy at all. Reporting
			// it as drift is what tells the operator that before the broker does.
			return a.brokerFailed(rep, name,
				"policy directory holds a file that is not a "+policyFileSuffix+" document",
				"the broker refuses the whole policy set when it finds one; remove or rename the file")
		}
		if fOwner != policyDocOwner || fGroup != policyDocGroup || !policyDocModes[fMode] {
			// The filename is a service name and would ordinarily be safe to
			// print, but a drifted directory is exactly where a name nobody
			// chose can appear. The shape of the drift is the finding.
			return a.brokerFailed(rep, name,
				fmt.Sprintf("a policy document is %s:%s %s, want %s:%s 0644", fOwner, fGroup, fMode, policyDocOwner, policyDocGroup),
				"the grant must be readable by the agent and writable only by root (ADR-0004 §4)")
		}
		content, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "cat", "--", path.Join(TorioMCPPolicyDir, file))
		if err != nil {
			return err
		}
		if content.exit != 0 {
			return a.brokerFailed(rep, name, "could not read a policy document", "verify the root-owned policy files on the guest")
		}
		documents[file] = []byte(content.out)
	}

	set, err := mcpbroker.ParseDocuments(documents)
	if err != nil {
		return a.brokerFailed(rep, name, "policy documents do not satisfy the strict broker schema", "repair the root-owned policy documents; the broker refuses a partially valid policy set")
	}
	rep.Policy = summarizePolicy(set)
	tools, writeTools := 0, 0
	for _, service := range rep.Policy.Services {
		tools += service.Tools
		writeTools += service.WriteTools
	}
	rep.record(name, true, fmt.Sprintf("%d service(s), %d tool(s), %d write tool(s); strict schema valid", len(rep.Policy.Services), tools, writeTools))
	return nil
}

// summarizePolicy reduces a parsed policy set to what a report carries.
//
// It counts rather than re-derives: the write total comes from the broker's own
// grant, which classified each tool when it validated the document. A summary
// that recounted the tools itself would be a second implementation of the rule
// the broker enforces, free to disagree with it.
//
// The order is the grant's, which is already sorted by service name, so two
// reports of one policy are identical without sorting again here.
func summarizePolicy(set mcpbroker.Set) PolicyGrant {
	grants := set.Grants()
	summary := PolicyGrant{
		Digest:   set.Digest(),
		Services: make([]PolicyService, 0, len(grants.Services)),
	}
	for _, service := range grants.Services {
		summary.Services = append(summary.Services, PolicyService{
			Name:             service.Name,
			UpstreamEndpoint: service.UpstreamEndpoint,
			Tools:            len(service.Tools),
			WriteTools:       service.WriteTools,
		})
	}
	return summary
}
