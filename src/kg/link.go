package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var fromID string
var toID string
var relType string
var linkScopeName string

var linkCmd = &cobra.Command{
	Use:   "link <from-id> --rel <RELTYPE> <to-id>",
	Short: "Create a relation between entities",
	Long:  `Create a relation between two entities. Writes to the default scope unless --scope is specified.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")

		scopeName := linkScopeName
		if scopeName == "" {
			defaultScope, _ := knowledge.GetDefaultScope(aiDir)
			scopeName = defaultScope
		}

		store, projectID, err := openStoreModeWithScope(false, scopeName)
		if err != nil {
			return err
		}
		defer store.Close()

		fromID := args[0]
		toID := args[1]
		if relType == "" {
			return fmt.Errorf("missing --rel flag")
		}
		if err := store.CreateRelation(fromID, toID, relType, projectID); err != nil {
			return err
		}
		fmt.Printf("Linked %s --%s--> %s\n", fromID, relType, toID)
		return nil
	},
}

func init() {
	linkCmd.Flags().StringVar(&relType, "rel", "", "Relation type")
	linkCmd.Flags().StringVar(&linkScopeName, "scope", "", "Scope to write to (default: default scope)")
	linkCmd.MarkFlagRequired("rel")
	rootCmd.AddCommand(linkCmd)
}
