package cli

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jzills/kx/internal/docs"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The resources: what the user can attach with @ in a client, and what an
// agent can read without a tool call. Each is a fixed URI rather than a
// template, because a client's @ menu lists fixed resources and may not list
// templates.
//
// Like the tools, they are read-only: each reads kx's own state, kubeconfig
// or embedded documentation, and none reaches the cluster. The ones that read
// state or kubeconfig run under locked, as every tool but docs and scan does,
// so a read never interleaves with a mark write or a discovery refresh.

const (
	resourceContextURI = "kx://context"
	resourceMarksURI   = "kx://marks"
	resourceListingURI = "kx://listing"
	resourceDocsPrefix = "kx://docs/"
	resourceJSON       = "application/json"
	resourceMarkdown   = "text/markdown"
)

func registerMCPResources(server *mcp.Server, deps mcpDeps) {
	server.AddResource(&mcp.Resource{
		URI:      resourceContextURI,
		Name:     "context",
		Title:    "Current kubeconfig context",
		MIMEType: resourceJSON,
		Description: "The kubeconfig context and namespace kx reads right now, and the kx version " +
			"serving them. The server follows kubeconfig live, so this is the cluster the next " +
			"tool call reaches.",
	}, deps.serializedResource(deps.contextResource))

	server.AddResource(&mcp.Resource{
		URI:      resourceMarksURI,
		Name:     "marks",
		Title:    "kx marks",
		MIMEType: resourceJSON,
		Description: "The marks: names the user pinned to resources with kx mark, or an agent with " +
			"the mark tool. Each is usable as a tool target's mark.",
	}, deps.serializedResource(deps.marksResource))

	server.AddResource(&mcp.Resource{
		URI:         resourceListingURI,
		Name:        "listing",
		Title:       "The user's current kx listing",
		MIMEType:    resourceJSON,
		Description: listingResourceDescription(deps.WriteListings),
	}, deps.serializedResource(deps.listingResource))

	// Not serialized, as the docs tool is not: the pages are embedded at build
	// time, and nothing a concurrent call does can change them.
	pages, err := docs.Topics()
	if err != nil {
		// The corpus is embedded, so this fails at build time in practice;
		// the docs tool reports the same error on its first call.
		return
	}
	for _, page := range pages {
		server.AddResource(&mcp.Resource{
			URI:         resourceDocsPrefix + page.Slug(),
			Name:        "docs/" + page.Slug(),
			Title:       page.Title,
			MIMEType:    resourceMarkdown,
			Description: page.Description,
		}, docsResource(page))
	}
}

// listingResourceDescription says what a row carries. Indexes only with
// --write-listings, for the same reason the tools return none without it: a
// client can read this resource on the agent's behalf as well as the user's,
// and with the flag off no index appears in anything the server returns.
func listingResourceDescription(writeListings bool) string {
	description := "The listing the user's kx commands resolve against now — what the last kx get, " +
		"kx diag, kx tree or kx top saved, or the one kx state moved to: the command that made it, " +
		"its context and namespace, and its rows in order, each with kind, name and namespace. "
	if writeListings {
		return description + "Each row carries its index, the number the user types in kx."
	}
	return description + "Rows carry no index; name a row to the user by kind and name."
}

// serializedResource is serialized for a resource handler.
func (d mcpDeps) serializedResource(handler mcp.ResourceHandler) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var result *mcp.ReadResourceResult
		err := d.locked(func() error {
			var err error
			result, err = handler(ctx, req)
			return err
		})
		return result, err
	}
}

// jsonResource is one JSON document as a resource's whole contents.
func jsonResource(uri string, document any) (*mcp.ReadResourceResult, error) {
	text, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: resourceJSON, Text: string(text),
	}}}, nil
}

type contextDocument struct {
	Version   string `json:"version"`
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	// WriteListings tells the agent whether what it lists becomes the user's
	// listing, and so whether rows carry indexes.
	WriteListings bool `json:"writeListings"`
}

func (d mcpDeps) contextResource(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return jsonResource(resourceContextURI, contextDocument{
		Version:       d.Version,
		Context:       d.Kubectl.CurrentContext(),
		Namespace:     d.Kubectl.CurrentNamespace(),
		WriteListings: d.WriteListings,
	})
}

func (d mcpDeps) marksResource(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	document, err := d.marksDocument()
	if err != nil {
		return nil, err
	}
	return jsonResource(resourceMarksURI, document)
}

// listingDocument is kx://listing. Context is the one kx reads now, beside
// the listing's own, so an agent can see that a listing taken on another
// cluster no longer matches where its tools reach.
type listingDocument struct {
	Context string            `json:"context"`
	Listing *listingEntryJSON `json:"listing"`
}

// listingEntryJSON is kx state --json's entry without its position in the
// stack, which is a number the user types (kx state 2) and so is left out
// with the indexes.
type listingEntryJSON struct {
	Context       string          `json:"context,omitempty"`
	Namespace     string          `json:"namespace,omitempty"`
	AllNamespaces bool            `json:"allNamespaces,omitempty"`
	Query         *stateQueryJSON `json:"query"`
	ByAgent       bool            `json:"byAgent,omitempty"`
	Resources     []listingRow    `json:"resources"`
}

// listingRow is stateRowJSON with the index optional: present only with
// --write-listings.
type listingRow struct {
	Index     int    `json:"index,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

func (d mcpDeps) listingResource(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	document := listingDocument{Context: d.Kubectl.CurrentContext()}
	entry, err := d.State.Load()
	if errors.Is(err, state.ErrNoState) {
		// No listing yet is an answer, not a failure: the user has not run
		// kx get, or cleared the stack.
		return jsonResource(resourceListingURI, document)
	}
	if err != nil {
		return nil, err
	}
	// The same conversion kx state --json makes, so the two cannot disagree
	// about a row's namespace or the query's spelling.
	full := stateJSONOf(entry, 0, 0)
	listing := &listingEntryJSON{
		Context: full.Context, Namespace: full.Namespace, AllNamespaces: full.AllNamespaces,
		Query: full.Query, ByAgent: full.ByAgent, Resources: make([]listingRow, 0, len(full.Resources)),
	}
	for _, row := range full.Resources {
		out := listingRow{Kind: string(row.Kind), Name: row.Name, Namespace: row.Namespace}
		if d.WriteListings {
			out.Index = row.Index
		}
		listing.Resources = append(listing.Resources, out)
	}
	document.Listing = listing
	return jsonResource(resourceListingURI, document)
}

// docsResource is one documentation page, as the Markdown it was written in
// with its title on top and the published URL after it.
func docsResource(page docs.Page) mcp.ResourceHandler {
	uri := resourceDocsPrefix + page.Slug()
	text := "# " + page.Title + "\n\n" + page.Body + "\n\n" + page.URL + "\n"
	return func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: resourceMarkdown, Text: text,
		}}}, nil
	}
}
