package knowledge

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/css"
	golang "github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/groovy"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/swift"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// langConfig describes how to interpret tree-sitter nodes for a given language.
type langConfig struct {
	Language        *sitter.Language
	FuncNodeTypes   []string // node types that represent function/method definitions
	TypeNodeTypes   []string // node types that represent class/struct/type definitions
	ImportNodeTypes []string // node types that represent import statements
	// NameField is the tree-sitter named-field that holds the identifier, almost always "name".
	NameField string
	// extractFuncName overrides the default name extraction for function nodes.
	// Needed for languages like C/C++ where the function name is nested under a declarator chain.
	// If nil, extractNodeName(node, src, cfg.NameField) is used.
	extractFuncName func(node *sitter.Node, src []byte) string
	// extractTypeName overrides the default name extraction for type nodes.
	// If nil, extractNodeName(node, src, cfg.NameField) is used.
	extractTypeName func(node *sitter.Node, src []byte) string
	// extractImportPath extracts the import path string(s) from an import node.
	// May be nil if the language has no imports.
	extractImportPath func(node *sitter.Node, src []byte) []string
	// isPublic filters symbols to only those considered public/exported.
	// If nil, all named symbols are included.
	isPublic func(name string) bool
}

// langRegistry maps lowercase file extensions (with leading dot) to langConfig.
var langRegistry map[string]langConfig

// extractFirstNamedChildOfType returns a function that scans named children for
// the first node of the given type and returns its text content. Used for
// grammars (e.g. Kotlin) that do not expose name fields via ChildByFieldName.
func extractFirstNamedChildOfType(nodeType string) func(*sitter.Node, []byte) string {
	return func(node *sitter.Node, src []byte) string {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == nodeType {
				return child.Content(src)
			}
		}
		return ""
	}
}

// extractDeclName navigates the declarator chain used by C/C++ function_definition nodes.
// The function name is nested: function_definition.declarator → function_declarator.declarator → identifier
func extractDeclName(node *sitter.Node, src []byte) string {
	decl := node.ChildByFieldName("declarator")
	for decl != nil {
		t := decl.Type()
		if t == "identifier" || t == "field_identifier" || t == "type_identifier" {
			return decl.Content(src)
		}
		decl = decl.ChildByFieldName("declarator")
	}
	return ""
}

// extractCInclude extracts the header path from a preproc_include node.
func extractCInclude(node *sitter.Node, src []byte) []string {
	// Prefer the structured "path" field (system_lib_string or string_literal)
	if path := node.ChildByFieldName("path"); path != nil {
		return []string{strings.Trim(path.Content(src), `"<>`)}
	}
	// Fallback: parse text — "#include <foo.h>" or "#include \"foo.h\""
	raw := strings.TrimSpace(node.Content(src))
	if idx := strings.Index(raw, " "); idx >= 0 {
		raw = strings.TrimSpace(raw[idx:])
		return []string{strings.Trim(raw, `"<>`)}
	}
	return nil
}

func init() {
	cCfg := langConfig{
		Language:          c.GetLanguage(),
		FuncNodeTypes:     []string{"function_definition"},
		TypeNodeTypes:     []string{"struct_specifier", "enum_specifier"},
		ImportNodeTypes:   []string{"preproc_include"},
		NameField:         "name",
		extractFuncName:   extractDeclName,
		extractImportPath: extractCInclude,
	}
	cppCfg := langConfig{
		Language:          cpp.GetLanguage(),
		FuncNodeTypes:     []string{"function_definition"},
		TypeNodeTypes:     []string{"class_specifier", "struct_specifier"},
		ImportNodeTypes:   []string{"preproc_include"},
		NameField:         "name",
		extractFuncName:   extractDeclName,
		extractImportPath: extractCInclude,
	}
	ktCfg := langConfig{
		Language:        kotlin.GetLanguage(),
		FuncNodeTypes:   []string{"function_declaration"},
		TypeNodeTypes:   []string{"class_declaration", "object_declaration", "interface_declaration"},
		ImportNodeTypes: []string{"import_header"},
		NameField:       "name", // fallback; overridden below by extractFuncName/extractTypeName
		extractFuncName: extractFirstNamedChildOfType("simple_identifier"),
		extractTypeName: extractFirstNamedChildOfType("type_identifier"),
		extractImportPath: func(node *sitter.Node, src []byte) []string {
			// import_header: "import kotlin.collections.List"
			// Strip the "import " keyword prefix from the full node content.
			raw := strings.TrimSpace(node.Content(src))
			raw = strings.TrimPrefix(raw, "import ")
			// Strip optional trailing alias: "... as Alias"
			if idx := strings.Index(raw, " as "); idx >= 0 {
				raw = raw[:idx]
			}
			return []string{strings.TrimSpace(raw)}
		},
	}

	langRegistry = map[string]langConfig{
		".go": {
			Language:        golang.GetLanguage(),
			FuncNodeTypes:   []string{"function_declaration", "method_declaration"},
			TypeNodeTypes:   []string{"type_spec"},
			ImportNodeTypes: []string{"import_spec"},
			NameField:       "name",
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				// import_spec has an interpreted_string_literal child
				for i := 0; i < int(node.NamedChildCount()); i++ {
					child := node.NamedChild(i)
					if child.Type() == "interpreted_string_literal" {
						raw := child.Content(src)
						if len(raw) >= 2 {
							raw = raw[1 : len(raw)-1]
						}
						return []string{raw}
					}
				}
				return nil
			},
			isPublic: func(name string) bool {
				return len(name) > 0 && unicode.IsUpper(rune(name[0]))
			},
		},
		".py": {
			Language:          python.GetLanguage(),
			FuncNodeTypes:     []string{"function_definition"},
			TypeNodeTypes:     []string{"class_definition"},
			ImportNodeTypes:   []string{"import_statement", "import_from_statement"},
			NameField:         "name",
			extractImportPath: extractPythonImports,
			isPublic: func(name string) bool {
				return !strings.HasPrefix(name, "_")
			},
		},
		".java": {
			Language:        java.GetLanguage(),
			FuncNodeTypes:   []string{"method_declaration", "constructor_declaration"},
			TypeNodeTypes:   []string{"class_declaration", "interface_declaration", "enum_declaration"},
			ImportNodeTypes: []string{"import_declaration"},
			NameField:       "name",
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				for i := 0; i < int(node.NamedChildCount()); i++ {
					child := node.NamedChild(i)
					t := child.Type()
					if t == "scoped_identifier" || t == "identifier" {
						return []string{child.Content(src)}
					}
				}
				// Fallback: "import java.util.List;" → strip prefix and ";"
				raw := strings.TrimSpace(node.Content(src))
				raw = strings.TrimPrefix(raw, "import ")
				raw = strings.TrimPrefix(raw, "static ")
				raw = strings.TrimSuffix(raw, ";")
				return []string{strings.TrimSpace(raw)}
			},
		},
		".kt":  ktCfg,
		".kts": ktCfg,
		".c":   cCfg,
		".h":   cCfg,
		".cpp": cppCfg,
		".cc":  cppCfg,
		".cxx": cppCfg,
		".hpp": cppCfg,
		".rs": {
			Language:        rust.GetLanguage(),
			FuncNodeTypes:   []string{"function_item"},
			TypeNodeTypes:   []string{"struct_item", "enum_item", "trait_item"},
			ImportNodeTypes: []string{"use_declaration"},
			NameField:       "name",
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				// use_declaration has an "argument" field with the use tree path
				if arg := node.ChildByFieldName("argument"); arg != nil {
					return []string{arg.Content(src)}
				}
				// Fallback: strip "use " prefix and ";" suffix
				raw := strings.TrimSpace(node.Content(src))
				raw = strings.TrimPrefix(raw, "use ")
				raw = strings.TrimSuffix(raw, ";")
				return []string{strings.TrimSpace(raw)}
			},
		},
		".swift": {
			Language:          swift.GetLanguage(),
			FuncNodeTypes:     []string{"function_declaration"},
			TypeNodeTypes:     []string{"class_declaration", "struct_declaration", "protocol_declaration"},
			ImportNodeTypes:   []string{"import_declaration"},
			NameField:         "name",
			extractImportPath: extractSwiftImports,
		},
		".rb": {
			Language:        ruby.GetLanguage(),
			FuncNodeTypes:   []string{"method", "singleton_method"},
			TypeNodeTypes:   []string{"class", "module"},
			ImportNodeTypes: []string{"call"},
			NameField:       "name",
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				// Only handle require / require_relative calls.
				method := node.ChildByFieldName("method")
				if method == nil {
					return nil
				}
				name := method.Content(src)
				if name != "require" && name != "require_relative" {
					return nil
				}
				// Extract the first string argument.
				args := node.ChildByFieldName("arguments")
				if args != nil {
					for i := 0; i < int(args.ChildCount()); i++ {
						child := args.Child(i)
						if child == nil {
							continue
						}
						t := child.Type()
						if t == "string" || t == "string_content" || t == "simple_symbol" {
							return []string{strings.Trim(child.Content(src), `"':`)}
						}
					}
				}
				// Fallback: parse text "require 'json'" → "json"
				raw := strings.TrimSpace(node.Content(src))
				parts := strings.Fields(raw)
				if len(parts) >= 2 {
					return []string{strings.Trim(parts[1], `"'`)}
				}
				return nil
			},
		},
		".cs": {
			Language:        csharp.GetLanguage(),
			FuncNodeTypes:   []string{"method_declaration", "constructor_declaration"},
			TypeNodeTypes:   []string{"class_declaration", "interface_declaration", "struct_declaration"},
			ImportNodeTypes: []string{"using_directive"},
			NameField:       "name",
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				for i := 0; i < int(node.NamedChildCount()); i++ {
					child := node.NamedChild(i)
					t := child.Type()
					if t == "qualified_name" || t == "identifier" || t == "name_equals" {
						return []string{child.Content(src)}
					}
				}
				// Fallback: strip "using " prefix and ";" suffix
				raw := strings.TrimSpace(node.Content(src))
				raw = strings.TrimPrefix(raw, "using static ")
				raw = strings.TrimPrefix(raw, "using ")
				raw = strings.TrimSuffix(raw, ";")
				return []string{strings.TrimSpace(raw)}
			},
		},
		".ts": {
			Language:          typescript.GetLanguage(),
			FuncNodeTypes:     []string{"function_declaration", "method_definition"},
			TypeNodeTypes:     []string{"class_declaration", "interface_declaration", "type_alias_declaration"},
			ImportNodeTypes:   []string{"import_statement"},
			NameField:         "name",
			extractImportPath: extractJSImports,
		},
		".tsx": {
			Language:          tsx.GetLanguage(),
			FuncNodeTypes:     []string{"function_declaration", "arrow_function", "method_definition"},
			TypeNodeTypes:     []string{"class_declaration", "interface_declaration"},
			ImportNodeTypes:   []string{"import_statement"},
			NameField:         "name",
			extractImportPath: extractJSImports,
		},
		".js": {
			Language:          javascript.GetLanguage(),
			FuncNodeTypes:     []string{"function_declaration", "method_definition", "arrow_function"},
			TypeNodeTypes:     []string{"class_declaration"},
			ImportNodeTypes:   []string{"import_statement"},
			NameField:         "name",
			extractImportPath: extractJSImports,
		},
		".jsx": {
			Language:          javascript.GetLanguage(),
			FuncNodeTypes:     []string{"function_declaration", "method_definition", "arrow_function"},
			TypeNodeTypes:     []string{"class_declaration"},
			ImportNodeTypes:   []string{"import_statement"},
			NameField:         "name",
			extractImportPath: extractJSImports,
		},

		// Bash / Shell — index function definitions and source includes
		".sh": {
			Language:        bash.GetLanguage(),
			FuncNodeTypes:   []string{"function_definition"},
			TypeNodeTypes:   []string{},
			ImportNodeTypes: []string{"command"},
			NameField:       "name",
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				// Only handle: source <path>  or  . <path>
				cmdNameNode := node.ChildByFieldName("name")
				if cmdNameNode == nil {
					return nil
				}
				cmdName := cmdNameNode.Content(src)
				if cmdName != "source" && cmdName != "." {
					return nil
				}
				// First word child (not inside command_name) is the script path
				for i := 0; i < int(node.NamedChildCount()); i++ {
					child := node.NamedChild(i)
					if child.Type() == "word" {
						return []string{child.Content(src)}
					}
				}
				return nil
			},
		},
		".bash": {}, // populated below

		// Groovy — Gradle build files, Jenkins pipelines
		".groovy": {
			Language:        groovy.GetLanguage(),
			FuncNodeTypes:   []string{"function_definition"},
			TypeNodeTypes:   []string{"class_definition"},
			ImportNodeTypes: []string{"groovy_import"},
			NameField:       "name",
			// function_definition in Groovy has no "name" field; name is first identifier child
			extractFuncName: extractFirstNamedChildOfType("identifier"),
			extractImportPath: func(node *sitter.Node, src []byte) []string {
				for i := 0; i < int(node.NamedChildCount()); i++ {
					if node.NamedChild(i).Type() == "qualified_name" {
						return []string{node.NamedChild(i).Content(src)}
					}
				}
				// Fallback: strip "import " prefix
				raw := strings.TrimSpace(node.Content(src))
				raw = strings.TrimPrefix(raw, "import ")
				return []string{strings.TrimSpace(raw)}
			},
		},

		// CSS — index rule-set selectors as "type" entities
		".css": {
			Language:      css.GetLanguage(),
			FuncNodeTypes: []string{},
			TypeNodeTypes: []string{"rule_set"},
			NameField:     "name",
			// rule_set has no named fields; selector text is in the "selectors" named child
			extractTypeName: func(node *sitter.Node, src []byte) string {
				for i := 0; i < int(node.NamedChildCount()); i++ {
					if node.NamedChild(i).Type() == "selectors" {
						return strings.TrimSpace(node.NamedChild(i).Content(src))
					}
				}
				return ""
			},
		},
	}

	// Share bash config for both extensions
	bashCfg := langRegistry[".sh"]
	langRegistry[".bash"] = bashCfg
}

// processWithTreeSitter parses the given file using tree-sitter and emits
// entities/relations into the provided CSV writers.
func (idx *Indexer) processWithTreeSitter(
	absPath, relPath string,
	cfg langConfig,
	entities *[]entityRecord,
	seenEntities map[string]bool,
	relations *[]relationRecord,
	stats *IndexStats,
) error {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", absPath, err)
	}

	parser := sitter.NewParser()
	parser.SetLanguage(cfg.Language)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", absPath, err)
	}
	defer tree.Close()

	now := time.Now().UTC()

	// Create file entity
	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seenEntities, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	idx.walkNode(tree.RootNode(), src, fileID, relPath, cfg, entities, seenEntities, relations, stats, now)
	return nil
}

// walkNode performs a depth-first traversal of the parse tree, extracting
// structural entities and relations.
func (idx *Indexer) walkNode(
	node *sitter.Node,
	src []byte,
	fileID, relPath string,
	cfg langConfig,
	entities *[]entityRecord,
	seenEntities map[string]bool,
	relations *[]relationRecord,
	stats *IndexStats,
	now time.Time,
) {
	nodeType := node.Type()

	switch {
	case nodeType == "package_clause":
		// Go package: child is package_identifier (not a named field "name")
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "package_identifier" {
				pkgName := child.Content(src)
				pkgID := fmt.Sprintf("package:%s", pkgName)
				if writeEntity(entities, seenEntities, pkgID, pkgName, EntityTypePackage, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: pkgID, Type: RelBelongsTo})
				stats.RelationsCreated++
				break
			}
		}

	case slices.Contains(cfg.FuncNodeTypes, nodeType):
		var name string
		if cfg.extractFuncName != nil {
			name = cfg.extractFuncName(node, src)
		} else {
			name = extractNodeName(node, src, cfg.NameField)
		}
		if name == "" {
			break
		}
		if cfg.isPublic != nil && !cfg.isPublic(name) {
			break
		}
		funcID := fmt.Sprintf("function:%s:%s", relPath, name)
		if writeEntity(entities, seenEntities, funcID, name, EntityTypeFunction, idx.projectID, now) {
			stats.EntitiesCreated++
		}
		*relations = append(*relations, relationRecord{FromID: fileID, ToID: funcID, Type: RelContains})
		stats.RelationsCreated++

	case slices.Contains(cfg.TypeNodeTypes, nodeType):
		var name string
		if cfg.extractTypeName != nil {
			name = cfg.extractTypeName(node, src)
		} else {
			name = extractNodeName(node, src, cfg.NameField)
		}
		if name == "" {
			break
		}
		if cfg.isPublic != nil && !cfg.isPublic(name) {
			break
		}
		typeID := fmt.Sprintf("type:%s:%s", relPath, name)
		if writeEntity(entities, seenEntities, typeID, name, EntityTypeType, idx.projectID, now) {
			stats.EntitiesCreated++
		}
		*relations = append(*relations, relationRecord{FromID: fileID, ToID: typeID, Type: RelContains})
		stats.RelationsCreated++

	case slices.Contains(cfg.ImportNodeTypes, nodeType):
		if cfg.extractImportPath != nil {
			paths := cfg.extractImportPath(node, src)
			for _, importPath := range paths {
				if importPath == "" {
					continue
				}
				importID := fmt.Sprintf("import:%s", importPath)
				if writeEntity(entities, seenEntities, importID, importPath, EntityTypeImport, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: importID, Type: RelImports})
				stats.RelationsCreated++
			}
		}
	}

	// Recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(i); child != nil {
			idx.walkNode(child, src, fileID, relPath, cfg, entities, seenEntities, relations, stats, now)
		}
	}
}

// extractNodeName retrieves the text of the named child field `fieldName` from node.
func extractNodeName(node *sitter.Node, src []byte, fieldName string) string {
	child := node.ChildByFieldName(fieldName)
	if child == nil {
		return ""
	}
	return child.Content(src)
}

// extractJSImports extracts the module path from a JS/TS import_statement node.
// Tries the "source" named field first; falls back to scanning for the last
// string/string_literal child (handles grammar versions where the field isn't exposed).
func extractJSImports(node *sitter.Node, src []byte) []string {
	if source := node.ChildByFieldName("source"); source != nil {
		return []string{stripStringLiteral(source.Content(src))}
	}
	// Fallback: scan children in reverse; the module path is the last string.
	for i := int(node.ChildCount()) - 1; i >= 0; i-- {
		child := node.Child(i)
		if child == nil {
			continue
		}
		t := child.Type()
		if t == "string" || t == "string_literal" || t == "template_string" {
			return []string{stripStringLiteral(child.Content(src))}
		}
	}
	return nil
}

// stripStringLiteral removes surrounding quote characters (', ", `) from s.
func stripStringLiteral(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') ||
			(first == '"' && last == '"') ||
			(first == '`' && last == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// extractPythonImports handles both import_statement and import_from_statement nodes.
func extractPythonImports(node *sitter.Node, src []byte) []string {
	var results []string
	content := node.Content(src)

	nodeType := node.Type()
	switch nodeType {
	case "import_statement":
		// e.g. "import os" or "import os, sys"
		// Walk named children to find dotted_name or identifier
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			t := child.Type()
			if t == "dotted_name" || t == "identifier" || t == "aliased_import" {
				// For aliased_import, get the original name
				if t == "aliased_import" {
					if orig := child.NamedChild(0); orig != nil {
						results = append(results, orig.Content(src))
					}
				} else {
					results = append(results, child.Content(src))
				}
			}
		}
	case "import_from_statement":
		// e.g. "from os import path" → extract "os"
		// The module_name field or first dotted_name child is the source
		if mod := node.ChildByFieldName("module_name"); mod != nil {
			results = append(results, mod.Content(src))
		} else {
			// fallback: parse text
			if strings.HasPrefix(content, "from ") {
				parts := strings.Fields(content)
				if len(parts) >= 2 {
					results = append(results, parts[1])
				}
			}
		}
	}
	return results
}

// extractSwiftImports extracts the module name from a Swift import_declaration.
// Different grammar versions expose the identifier differently; this tries named
// children first and falls back to text parsing.
func extractSwiftImports(node *sitter.Node, src []byte) []string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		t := child.Type()
		content := child.Content(src)
		// Skip import_kind modifiers ("class", "struct", etc.) and attributes.
		if t == "import_kind" || t == "attribute" || content == "import" {
			continue
		}
		// Any identifier-like or dotted node is the module path.
		if t == "identifier" || t == "scoped_identifier" || t == "dot_expression" ||
			t == "member_expression" || t == "type_identifier" || strings.HasSuffix(t, "identifier") {
			return []string{content}
		}
	}
	// Fallback: strip "import " prefix and optional import kind keyword.
	content := strings.TrimSpace(node.Content(src))
	if strings.HasPrefix(content, "import ") {
		rest := strings.TrimSpace(content[len("import "):])
		// Strip optional kind keyword (e.g. "import class Foundation" → "Foundation")
		for _, kind := range []string{"class ", "struct ", "enum ", "protocol ", "typealias ", "func ", "var ", "let "} {
			rest = strings.TrimPrefix(rest, kind)
		}
		return []string{strings.TrimSpace(rest)}
	}
	return nil
}

