package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// The knowledge CLI is the agent-facing write path into the workspace
// knowledge graph (docs/knowledge-graph-plan.md). Output defaults to JSON:
// agents are the primary consumer.

var knowledgeCmd = &cobra.Command{
	Use:   "knowledge",
	Short: "Work with the workspace knowledge graph",
}

var knowledgeSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search knowledge nodes by slug, title, or alias (search before creating)",
	Args:  exactArgs(1),
	RunE:  runKnowledgeSearch,
}

var knowledgeNodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage knowledge nodes (graph vertices / wiki pages)",
}

var knowledgeNodeAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Create a knowledge node (409s with candidates on likely duplicates)",
	RunE:  runKnowledgeNodeAdd,
}

var knowledgeNodeGetCmd = &cobra.Command{
	Use:   "get <slug-or-id>",
	Short: "Get a knowledge node",
	Args:  exactArgs(1),
	RunE:  runKnowledgeNodeGet,
}

var knowledgeNodeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List knowledge nodes",
	RunE:  runKnowledgeNodeList,
}

var knowledgeEdgeCmd = &cobra.Command{
	Use:   "edge",
	Short: "Manage knowledge edges (typed facts between nodes/entities)",
}

var knowledgeEdgeAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Assert a fact: identical live facts are affirmed, not duplicated",
	RunE:  runKnowledgeEdgeAdd,
}

var knowledgeEdgeCloseCmd = &cobra.Command{
	Use:   "close <edge-id>",
	Short: "End a fact's validity window (members only)",
	Args:  exactArgs(1),
	RunE:  runKnowledgeEdgeClose,
}

var knowledgeGraphCmd = &cobra.Command{
	Use:   "graph <node-slug-or-id>",
	Short: "Bounded N-hop neighborhood around a focus node",
	Args:  exactArgs(1),
	RunE:  runKnowledgeGraph,
}

var knowledgePathCmd = &cobra.Command{
	Use:   "path <src> <dst>",
	Short: "Find how two nodes are connected (multi-hop shortest path)",
	Args:  exactArgs(2),
	RunE:  runKnowledgePath,
}

func runKnowledgeSearch(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	params := url.Values{}
	params.Set("q", args[0])
	if limit, _ := cmd.Flags().GetInt("limit"); limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/knowledge/search?"+params.Encode(), &result); err != nil {
		return fmt.Errorf("search knowledge: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgeNodeAdd(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	kind, _ := cmd.Flags().GetString("kind")
	title, _ := cmd.Flags().GetString("title")
	if strings.TrimSpace(kind) == "" || strings.TrimSpace(title) == "" {
		return fmt.Errorf("--kind and --title are required")
	}
	body := map[string]any{
		"kind":  kind,
		"title": title,
	}
	if v, _ := cmd.Flags().GetString("slug"); strings.TrimSpace(v) != "" {
		body["slug"] = v
	}
	if v, _ := cmd.Flags().GetString("summary"); strings.TrimSpace(v) != "" {
		body["summary"] = v
	}
	if v, _ := cmd.Flags().GetString("content"); strings.TrimSpace(v) != "" {
		body["content"] = v
	}
	if aliases, _ := cmd.Flags().GetStringArray("alias"); len(aliases) > 0 {
		body["aliases"] = aliases
	}
	if v, _ := cmd.Flags().GetString("attrs"); strings.TrimSpace(v) != "" {
		body["attrs"] = jsonRawFlag(v)
	}
	if confirmNew, _ := cmd.Flags().GetBool("confirm-new"); confirmNew {
		body["confirm_new"] = true
	}

	var result map[string]any
	if err := client.PostJSON(ctx, "/api/knowledge/nodes", body, &result); err != nil {
		return fmt.Errorf("create knowledge node: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgeNodeGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var result map[string]any
	if err := client.GetJSON(ctx, "/api/knowledge/nodes/"+url.PathEscape(args[0]), &result); err != nil {
		return fmt.Errorf("get knowledge node: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgeNodeList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	params := url.Values{}
	if v, _ := cmd.Flags().GetString("kind"); strings.TrimSpace(v) != "" {
		params.Set("kind", v)
	}
	if v, _ := cmd.Flags().GetString("status"); strings.TrimSpace(v) != "" {
		params.Set("status", v)
	}
	if limit, _ := cmd.Flags().GetInt("limit"); limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/knowledge/nodes"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	var result map[string]any
	if err := client.GetJSON(ctx, path, &result); err != nil {
		return fmt.Errorf("list knowledge nodes: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgeEdgeAdd(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	src, _ := cmd.Flags().GetString("src")
	dst, _ := cmd.Flags().GetString("dst")
	predicate, _ := cmd.Flags().GetString("predicate")
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" || strings.TrimSpace(predicate) == "" {
		return fmt.Errorf("--src, --dst, and --predicate are required")
	}
	srcType, _ := cmd.Flags().GetString("src-type")
	dstType, _ := cmd.Flags().GetString("dst-type")
	body := map[string]any{
		"src_type": srcType, "src_id": src,
		"dst_type": dstType, "dst_id": dst,
		"predicate": predicate,
	}
	if cmd.Flags().Changed("confidence") {
		confidence, _ := cmd.Flags().GetFloat32("confidence")
		body["confidence"] = confidence
	}
	if v, _ := cmd.Flags().GetString("source-id"); strings.TrimSpace(v) != "" {
		body["source_id"] = v
	}
	if v, _ := cmd.Flags().GetString("source-url"); strings.TrimSpace(v) != "" {
		body["source_url"] = v
	}
	if v, _ := cmd.Flags().GetString("source-title"); strings.TrimSpace(v) != "" {
		body["source_title"] = v
	}
	if v, _ := cmd.Flags().GetString("note"); strings.TrimSpace(v) != "" {
		body["note"] = v
	}
	if v, _ := cmd.Flags().GetString("attrs"); strings.TrimSpace(v) != "" {
		body["attrs"] = jsonRawFlag(v)
	}
	if supersede, _ := cmd.Flags().GetBool("supersede"); supersede {
		body["supersede"] = true
	}

	var result map[string]any
	if err := client.PostJSON(ctx, "/api/knowledge/edges", body, &result); err != nil {
		return fmt.Errorf("create knowledge edge: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgeEdgeClose(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	body := map[string]any{}
	if v, _ := cmd.Flags().GetString("superseded-by"); strings.TrimSpace(v) != "" {
		body["superseded_by"] = v
	}
	var result map[string]any
	if err := client.PostJSON(ctx, "/api/knowledge/edges/"+url.PathEscape(args[0])+"/close", body, &result); err != nil {
		return fmt.Errorf("close knowledge edge: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgeGraph(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	params := url.Values{}
	params.Set("focus", args[0])
	if v, _ := cmd.Flags().GetString("type"); strings.TrimSpace(v) != "" {
		params.Set("focus_type", v)
	}
	if hops, _ := cmd.Flags().GetInt("hops"); hops > 0 {
		params.Set("hops", fmt.Sprintf("%d", hops))
	}
	if includeProposed, _ := cmd.Flags().GetBool("include-proposed"); includeProposed {
		params.Set("include_proposed", "true")
	}
	if v, _ := cmd.Flags().GetString("as-of"); strings.TrimSpace(v) != "" {
		params.Set("as_of", v)
	}
	if cmd.Flags().Changed("min-confidence") {
		minConfidence, _ := cmd.Flags().GetFloat32("min-confidence")
		params.Set("min_confidence", fmt.Sprintf("%g", minConfidence))
	}
	if limit, _ := cmd.Flags().GetInt("limit"); limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/knowledge/graph?"+params.Encode(), &result); err != nil {
		return fmt.Errorf("expand knowledge graph: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runKnowledgePath(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	params := url.Values{}
	params.Set("src", args[0])
	params.Set("dst", args[1])
	if v, _ := cmd.Flags().GetString("src-type"); strings.TrimSpace(v) != "" {
		params.Set("src_type", v)
	}
	if v, _ := cmd.Flags().GetString("dst-type"); strings.TrimSpace(v) != "" {
		params.Set("dst_type", v)
	}
	if maxHops, _ := cmd.Flags().GetInt("max-hops"); maxHops > 0 {
		params.Set("max_hops", fmt.Sprintf("%d", maxHops))
	}
	if includeProposed, _ := cmd.Flags().GetBool("include-proposed"); includeProposed {
		params.Set("include_proposed", "true")
	}
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/knowledge/path?"+params.Encode(), &result); err != nil {
		return fmt.Errorf("find knowledge path: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

// jsonRawFlag lets a --attrs '{"k":"v"}' flag pass through as a JSON value
// rather than a quoted string.
func jsonRawFlag(v string) any {
	return jsonRaw(v)
}

type jsonRaw string

func (j jsonRaw) MarshalJSON() ([]byte, error) {
	return []byte(j), nil
}

func init() {
	knowledgeCmd.AddCommand(knowledgeSearchCmd)
	knowledgeCmd.AddCommand(knowledgeNodeCmd)
	knowledgeCmd.AddCommand(knowledgeEdgeCmd)
	knowledgeCmd.AddCommand(knowledgeGraphCmd)
	knowledgeCmd.AddCommand(knowledgePathCmd)

	knowledgeNodeCmd.AddCommand(knowledgeNodeAddCmd)
	knowledgeNodeCmd.AddCommand(knowledgeNodeGetCmd)
	knowledgeNodeCmd.AddCommand(knowledgeNodeListCmd)

	knowledgeEdgeCmd.AddCommand(knowledgeEdgeAddCmd)
	knowledgeEdgeCmd.AddCommand(knowledgeEdgeCloseCmd)

	knowledgeSearchCmd.Flags().Int("limit", 20, "Maximum results")

	knowledgeNodeAddCmd.Flags().String("kind", "", "Node kind (person, organization, concept, ... — required)")
	knowledgeNodeAddCmd.Flags().String("title", "", "Canonical title (required)")
	knowledgeNodeAddCmd.Flags().String("slug", "", "Explicit slug (defaults to a kebab-case of the title)")
	knowledgeNodeAddCmd.Flags().StringArray("alias", nil, "Alias (may be repeated)")
	knowledgeNodeAddCmd.Flags().String("summary", "", "One-line summary for graph tooltips")
	knowledgeNodeAddCmd.Flags().String("content", "", "Markdown body (the node's wiki page)")
	knowledgeNodeAddCmd.Flags().String("attrs", "", "JSON object of structured attributes")
	knowledgeNodeAddCmd.Flags().Bool("confirm-new", false, "Create even when duplicate candidates exist")

	knowledgeNodeListCmd.Flags().String("kind", "", "Filter by kind")
	knowledgeNodeListCmd.Flags().String("status", "", "Filter by status (proposed, confirmed, rejected)")
	knowledgeNodeListCmd.Flags().Int("limit", 100, "Maximum results")

	knowledgeEdgeAddCmd.Flags().String("src", "", "Source endpoint: node slug/UUID, or entity UUID with --src-type (required)")
	knowledgeEdgeAddCmd.Flags().String("src-type", "node", "Source endpoint type: node, issue, project, agent, member")
	knowledgeEdgeAddCmd.Flags().String("dst", "", "Destination endpoint (required)")
	knowledgeEdgeAddCmd.Flags().String("dst-type", "node", "Destination endpoint type")
	knowledgeEdgeAddCmd.Flags().String("predicate", "", "Relationship predicate, e.g. works_at, authored, influenced (required)")
	knowledgeEdgeAddCmd.Flags().Float32("confidence", 0, "Explicit confidence in (0, 1]")
	knowledgeEdgeAddCmd.Flags().String("source-id", "", "Provenance: existing knowledge source UUID")
	knowledgeEdgeAddCmd.Flags().String("source-url", "", "Provenance: source URL (deduplicated per workspace)")
	knowledgeEdgeAddCmd.Flags().String("source-title", "", "Provenance: title for a new URL source")
	knowledgeEdgeAddCmd.Flags().String("note", "", "Evidence note")
	knowledgeEdgeAddCmd.Flags().String("attrs", "", "JSON object of structured attributes")
	knowledgeEdgeAddCmd.Flags().Bool("supersede", false, "Close a conflicting single-valued fact in favor of this one (members only)")

	knowledgeEdgeCloseCmd.Flags().String("superseded-by", "", "UUID of the edge replacing this fact")

	knowledgeGraphCmd.Flags().String("type", "node", "Focus endpoint type")
	knowledgeGraphCmd.Flags().Int("hops", 2, "Expansion depth (1-4)")
	knowledgeGraphCmd.Flags().Bool("include-proposed", false, "Include unreviewed proposed edges")
	knowledgeGraphCmd.Flags().String("as-of", "", "Render the graph as of an RFC3339 instant")
	knowledgeGraphCmd.Flags().Float32("min-confidence", 0, "Minimum edge confidence")
	knowledgeGraphCmd.Flags().Int("limit", 200, "Maximum edges (1-500)")

	knowledgePathCmd.Flags().String("src-type", "node", "Source endpoint type")
	knowledgePathCmd.Flags().String("dst-type", "node", "Destination endpoint type")
	knowledgePathCmd.Flags().Int("max-hops", 4, "Maximum path length (1-4)")
	knowledgePathCmd.Flags().Bool("include-proposed", false, "Include unreviewed proposed edges")
}
