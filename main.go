package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/major1201/dante/pkg/log"
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
	)

	cmd := &cobra.Command{
		Use:          "kanorama",
		Short:        "Kubernetes cluster report generator",
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
			for _, mo := range selected {
				mo.Init(ctx)
				name := mo.Name()
				if err := mo.Run(); err != nil {
					log.L.Error(err, "run module failed", "name", name)
				}
				fmt.Fprintf(out, "======================= %s =======================\n", name)
				mo.Print(out)
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
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tName\tEnabled by default")
	for _, mo := range modules.ModuleList {
		fmt.Fprintf(tw, "%s\t%s\t%t\n", mo.ID(), mo.Name(), mo.EnableByDefault())
	}
	return tw.Flush()
}
