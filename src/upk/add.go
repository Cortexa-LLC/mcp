package main

import (
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/kglib"
	"github.com/cortexa-llc/mcp/upk/internal/upk"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add content to your personal knowledge graph",
	Long:  "Add conversations, learnings, insights, and other knowledge to your personal graph",
}

var addConversationCmd = &cobra.Command{
	Use:   "conversation",
	Short: "Record a conversation",
	Long:  "Record a conversation with title, summary, and optional topics/participants",
	Run: func(cmd *cobra.Command, args []string) {
		title, _ := cmd.Flags().GetString("title")
		summary, _ := cmd.Flags().GetString("summary")

		if title == "" {
			fmt.Fprintln(os.Stderr, "Error: --title is required")
			os.Exit(1)
		}
		if summary == "" {
			fmt.Fprintln(os.Stderr, "Error: --summary is required")
			os.Exit(1)
		}

		// Open user store
		dbPath, err := upk.GetUserDBPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		schemaCfg := &kglib.SchemaConfig{
			AdditionalRelTypes: upk.AllRelTypes,
		}
		store, err := kglib.OpenStore(dbPath, schemaCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run 'upk init' first to initialize the database")
			os.Exit(1)
		}
		defer store.Close()

		// Create conversation entity
		conv, err := store.CreateEntity(title, upk.EntityTypeConversation, "user")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating conversation: %v\n", err)
			os.Exit(1)
		}

		// Add summary as observation
		if _, err := store.CreateObservation(conv.ID, summary, "user"); err != nil {
			fmt.Fprintf(os.Stderr, "Error adding summary: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created conversation %s (%s)\n", title, conv.ID)
	},
}

var addLearningCmd = &cobra.Command{
	Use:   "learning",
	Short: "Record a learning or insight",
	Long:  "Record a learning, insight, or piece of knowledge from conversations, reading, or work",
	Run: func(cmd *cobra.Command, args []string) {
		content, _ := cmd.Flags().GetString("content")
		source, _ := cmd.Flags().GetString("source")
		tags, _ := cmd.Flags().GetStringSlice("tags")

		if content == "" {
			fmt.Fprintln(os.Stderr, "Error: --content is required")
			os.Exit(1)
		}

		// Open user store
		dbPath, err := upk.GetUserDBPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		schemaCfg := &kglib.SchemaConfig{
			AdditionalRelTypes: upk.AllRelTypes,
		}
		store, err := kglib.OpenStore(dbPath, schemaCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run 'upk init' first to initialize the database")
			os.Exit(1)
		}
		defer store.Close()

		// Create learning entity
		learning, err := store.CreateEntity(content, upk.EntityTypeLearning, "user")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating learning: %v\n", err)
			os.Exit(1)
		}

		// Add source as observation if provided
		if source != "" {
			if _, err := store.CreateObservation(learning.ID, fmt.Sprintf("Source: %s", source), "user"); err != nil {
				fmt.Fprintf(os.Stderr, "Error adding source: %v\n", err)
				os.Exit(1)
			}
		}

		// Add tags as topics and relations
		for _, tag := range tags {
			topic, err := store.CreateEntity(tag, upk.EntityTypeTopic, "user")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create topic %s: %v\n", tag, err)
				continue
			}
			if err := store.CreateRelation(learning.ID, topic.ID, upk.RelTaggedWith, "user"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to link tag %s: %v\n", tag, err)
			}
		}

		fmt.Printf("Created learning %s\n", learning.ID)
		if len(tags) > 0 {
			fmt.Printf("Tagged with: %v\n", tags)
		}
	},
}

func init() {
	// Add subcommands
	addCmd.AddCommand(addConversationCmd)
	addCmd.AddCommand(addLearningCmd)

	// Conversation flags
	addConversationCmd.Flags().StringP("title", "t", "", "Conversation title (required)")
	addConversationCmd.Flags().StringP("summary", "s", "", "Conversation summary (required)")

	// Learning flags
	addLearningCmd.Flags().StringP("content", "c", "", "Learning content (required)")
	addLearningCmd.Flags().String("source", "", "Source of the learning")
	addLearningCmd.Flags().StringSlice("tags", []string{}, "Tags/topics for the learning")
}
