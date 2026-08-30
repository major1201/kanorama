package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/major1201/kanorama/modules"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(2)
	}
}

func newRootCommand() *cobra.Command {
	var (
		onlyModules    string
		enableModules  string
		disableModules string
		listModules    bool
		kubeconfigPath string
		contextName    string
		htmlOutput     string
	)

	cmd := &cobra.Command{
		Use:          "kanorama",
		Short:        "Kubernetes panorama",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			modules.SetClientConfig(kubeconfigPath, contextName)

			if listModules {
				return printModuleList(cmd.OutOrStdout())
			}

			selected, err := selectModules(splitIDs(onlyModules), splitIDs(enableModules), splitIDs(disableModules))
			if err != nil {
				return err
			}

			ctx := context.Background()
			out := cmd.OutOrStdout()

			reports := make([]moduleReport, 0, len(selected))
			for _, mo := range selected {
				mo.Init(ctx)
				name := mo.Name()
				if err := mo.Run(); err != nil {
					slog.Error("run module failed", "name", name, "error", err)
				}
				var buf strings.Builder
				mo.Print(&buf)
				reports = append(reports, moduleReport{Name: name, Content: buf.String()})
			}

			if htmlOutput != "" {
				if err := writeHTMLReport(htmlOutput, reports); err != nil {
					return err
				}
				fmt.Fprintf(out, "Report written to %s\n", htmlOutput)
				return nil
			}

			for _, r := range reports {
				fmt.Fprintf(out, "======================= %s =======================\n", r.Name)
				io.WriteString(out, r.Content)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&onlyModules, "modules", "m", "", "comma-separated list of module IDs to run (replaces the default-enabled set)")
	cmd.Flags().StringVarP(&enableModules, "enable-modules", "E", "", "comma-separated list of module IDs to enable (in addition to default-enabled modules)")
	cmd.Flags().StringVarP(&disableModules, "disable-modules", "D", "", "comma-separated list of module IDs to disable")
	cmd.Flags().BoolVarP(&listModules, "list-modules", "L", false, "list all modules and whether they are enabled by default")
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "", "path to the kubeconfig file")
	cmd.Flags().StringVar(&contextName, "context", "", "name of the kubeconfig context to use")
	cmd.Flags().StringVar(&htmlOutput, "html", "", "write the report to an HTML file with one tab per module")

	cmd.SetHelpTemplate(cmd.HelpTemplate() + "\nModules:\n" + moduleListString())

	return cmd
}

func splitIDs(s string) []string {
	var ids []string
	for _, part := range strings.Split(s, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func selectModules(onlyIDs, enableIDs, disableIDs []string) ([]modules.Module, error) {
	byID := make(map[string]modules.Module, len(modules.ModuleList))
	for _, mo := range modules.ModuleList {
		byID[mo.ID()] = mo
	}

	validate := func(ids []string) error {
		for _, id := range ids {
			if _, ok := byID[id]; !ok {
				return fmt.Errorf("unknown module %q", id)
			}
		}
		return nil
	}
	if err := validate(onlyIDs); err != nil {
		return nil, err
	}
	if err := validate(enableIDs); err != nil {
		return nil, err
	}
	if err := validate(disableIDs); err != nil {
		return nil, err
	}

	selected := make([]modules.Module, 0, len(modules.ModuleList))
	seen := make(map[string]bool)

	// --modules is an explicit allowlist; otherwise start from every
	// default-enabled module.
	if len(onlyIDs) > 0 {
		for _, id := range onlyIDs {
			if !seen[id] {
				selected = append(selected, byID[id])
				seen[id] = true
			}
		}
	} else {
		for _, mo := range modules.ModuleList {
			if mo.EnableByDefault() {
				selected = append(selected, mo)
				seen[mo.ID()] = true
			}
		}
	}

	// Enable additionally requested modules on top of the base set.
	for _, id := range enableIDs {
		if !seen[id] {
			selected = append(selected, byID[id])
			seen[id] = true
		}
	}

	// Disable requested modules; this wins over --enable-modules.
	if len(disableIDs) > 0 {
		disabled := make(map[string]bool, len(disableIDs))
		for _, id := range disableIDs {
			disabled[id] = true
		}

		filtered := selected[:0]
		for _, mo := range selected {
			if !disabled[mo.ID()] {
				filtered = append(filtered, mo)
			}
		}
		selected = filtered
	}

	return selected, nil
}

func printModuleList(w io.Writer) error {
	writeModuleList(w)
	return nil
}

func writeModuleList(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tName\tEnabled by default")
	for _, mo := range modules.ModuleList {
		fmt.Fprintf(tw, "%s\t%s\t%t\n", mo.ID(), mo.Name(), mo.EnableByDefault())
	}
	tw.Flush()
}

func moduleListString() string {
	var b strings.Builder
	writeModuleList(&b)
	return b.String()
}

type moduleReport struct {
	Name    string
	Content string
}

func writeHTMLReport(path string, reports []moduleReport) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<title>Kanorama Report</title>\n")
	b.WriteString(`<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; margin: 0; color: #1f2328; }
.tabs { display: flex; flex-wrap: wrap; border-bottom: 1px solid #d0d7de; background: #f6f8fa; position: sticky; top: 0; }
.tab { padding: 10px 16px; cursor: pointer; border: none; background: none; font-size: 14px; color: #24292f; }
.tab:hover { background: #eaeef2; }
.tab.active { box-shadow: inset 0 -2px 0 #0969da; color: #0969da; font-weight: 600; }
.panel { display: none; padding: 16px; }
.panel.active { display: block; }
pre { font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace; font-size: 13px; line-height: 1.45; overflow-x: auto; margin: 0; white-space: pre; }
</style>
</head>
<body>
`)

	b.WriteString("<div class=\"tabs\">\n")
	for i, r := range reports {
		fmt.Fprintf(&b, "  <button class=\"tab%s\" id=\"tab-%d\" onclick=\"showTab(%d, this)\">%s</button>\n",
			activeClass(i), i, i, html.EscapeString(r.Name))
	}
	b.WriteString("</div>\n")

	for i, r := range reports {
		fmt.Fprintf(&b, "<div class=\"panel%s\" id=\"panel-%d\"><pre>%s</pre></div>\n",
			activeClass(i), i, html.EscapeString(r.Content))
	}

	b.WriteString(`<script>
function showTab(i, el) {
  var tabs = document.querySelectorAll('.tab');
  for (var t = 0; t < tabs.length; t++) tabs[t].classList.remove('active');
  var panels = document.querySelectorAll('.panel');
  for (var p = 0; p < panels.length; p++) panels[p].classList.remove('active');
  el.classList.add('active');
  document.getElementById('panel-' + i).classList.add('active');
}
</script>
</body>
</html>
`)

	_, err = io.WriteString(f, b.String())
	return err
}

func activeClass(i int) string {
	if i == 0 {
		return " active"
	}
	return ""
}
