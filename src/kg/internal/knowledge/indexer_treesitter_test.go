package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// entityExistsByName queries the store for an entity with the given name and
// optionally the given entity type. Pass empty string for entityType to match
// any type.
func entityExistsByName(t *testing.T, store *Store, name, entityType string) bool {
	t.Helper()
	var q string
	if entityType != "" {
		q = fmt.Sprintf(`MATCH (e:Entity {name: "%s", type: "%s"}) RETURN count(e)`, name, entityType)
	} else {
		q = fmt.Sprintf(`MATCH (e:Entity {name: "%s"}) RETURN count(e)`, name)
	}
	result, err := store.Query(q)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	defer result.Close()
	if result.HasNext() {
		row, err := result.Next()
		if err != nil {
			t.Fatalf("result.Next: %v", err)
		}
		cnt, _ := row.GetValue(uint64(0))
		switch v := cnt.(type) {
		case int64:
			return v > 0
		case uint64:
			return v > 0
		}
	}
	return false
}

// relationExists queries for a relation of the given type between two entities
// identified by their names.
func relationExists(t *testing.T, store *Store, fromName, toName, relType string) bool {
	t.Helper()
	q := fmt.Sprintf(
		`MATCH (a:Entity {name: "%s"})-[r:%s]->(b:Entity {name: "%s"}) RETURN count(r)`,
		fromName, relType, toName,
	)
	result, err := store.Query(q)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	defer result.Close()
	if result.HasNext() {
		row, err := result.Next()
		if err != nil {
			t.Fatalf("result.Next: %v", err)
		}
		cnt, _ := row.GetValue(uint64(0))
		switch v := cnt.(type) {
		case int64:
			return v > 0
		case uint64:
			return v > 0
		}
	}
	return false
}

// runIndexer is a helper that creates a store + indexer, runs Index(), and
// returns the store, stats, and a cleanup function.
func runIndexer(t *testing.T, srcDir, dbPath string) (*Store, *IndexStats) {
	t.Helper()
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	idx, err := NewIndexer(store, "test-project", srcDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}
	return store, stats
}

// TestIndexer_GoFile_TreeSitter verifies that the tree-sitter Go parser
// extracts exported functions, exported types, and emits a BELONGS_TO
// relation from the file entity to the package entity.
func TestIndexer_GoFile_TreeSitter(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	goContent := `package mypkg

import "fmt"

// ExportedFunc does something.
func ExportedFunc(x int) string {
	return fmt.Sprintf("%d", x)
}

func unexported() {}

type ExportedType struct {
	Name string
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "example.go"), []byte(goContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	// Exported function must be extracted.
	if !entityExistsByName(t, store, "ExportedFunc", "function") {
		t.Error("expected entity 'ExportedFunc' (function) to be created")
	}

	// Exported type must be extracted.
	if !entityExistsByName(t, store, "ExportedType", "type") {
		t.Error("expected entity 'ExportedType' (type) to be created")
	}

	// Package entity must be created.
	if !entityExistsByName(t, store, "mypkg", "package") {
		t.Error("expected entity 'mypkg' (package) to be created")
	}

	// File → package BELONGS_TO relation must exist.
	// The indexer roots at srcDir, so the file entity name is "example.go" (not "src/example.go").
	if !relationExists(t, store, "example.go", "mypkg", "BELONGS_TO") {
		t.Error("expected BELONGS_TO relation from 'example.go' to 'mypkg'")
	}
}

// TestIndexer_PythonFile verifies extraction of a Python file: 1 class,
// 1 function and 1 import.
func TestIndexer_PythonFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	pyContent := `import os

class MyClass:
    def __init__(self):
        pass

def my_function(x):
    return os.path.join(x, "hello")
`
	if err := os.WriteFile(filepath.Join(srcDir, "example.py"), []byte(pyContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "MyClass", "type") {
		t.Error("expected entity 'MyClass' (type) to be created")
	}
	if !entityExistsByName(t, store, "my_function", "function") {
		t.Error("expected entity 'my_function' (function) to be created")
	}
	if !entityExistsByName(t, store, "os", "import") {
		t.Error("expected entity 'os' (import) to be created")
	}
}

// TestIndexer_JavaFile verifies extraction of a Java file: 1 class, 1 method
// and 1 import.
func TestIndexer_JavaFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	javaContent := `import java.util.List;

public class Greeter {
    public String greet(String name) {
        return "Hello, " + name;
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "Greeter.java"), []byte(javaContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Greeter", "type") {
		t.Error("expected entity 'Greeter' (type) to be created")
	}
	if !entityExistsByName(t, store, "greet", "function") {
		t.Error("expected entity 'greet' (function) to be created")
	}
	if !entityExistsByName(t, store, "java.util.List", "import") {
		t.Error("expected entity 'java.util.List' (import) to be created")
	}
}

// TestIndexer_KotlinFile verifies extraction of a Kotlin file: 1 class, 1
// function and 1 import.
func TestIndexer_KotlinFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	ktContent := `import kotlin.collections.List

class Greeter {
    fun greet(name: String): String {
        return "Hello, $name"
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "Greeter.kt"), []byte(ktContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Greeter", "type") {
		t.Error("expected entity 'Greeter' (type) to be created")
	}
	if !entityExistsByName(t, store, "greet", "function") {
		t.Error("expected entity 'greet' (function) to be created")
	}
	if !entityExistsByName(t, store, "kotlin.collections.List", "import") {
		t.Error("expected entity 'kotlin.collections.List' (import) to be created")
	}
}

// TestIndexer_CppFile verifies extraction of a C++ file: 1 struct, 1
// function and 1 include.
func TestIndexer_CppFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cppContent := `#include <iostream>

struct Point {
    int x;
    int y;
};

void printPoint(Point p) {
    std::cout << p.x << "," << p.y << std::endl;
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "point.cpp"), []byte(cppContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Point", "type") {
		t.Error("expected entity 'Point' (type) to be created")
	}
	if !entityExistsByName(t, store, "printPoint", "function") {
		t.Error("expected entity 'printPoint' (function) to be created")
	}
	if !entityExistsByName(t, store, "iostream", "import") {
		t.Error("expected entity 'iostream' (import) to be created")
	}
}

// TestIndexer_CFile verifies extraction of a C file: 1 struct, 1 function and
// 1 include.
func TestIndexer_CFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cContent := `#include <stdio.h>

struct Point {
    int x;
    int y;
};

void printPoint(struct Point p) {
    printf("%d,%d\n", p.x, p.y);
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "point.c"), []byte(cContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Point", "type") {
		t.Error("expected entity 'Point' (type) to be created")
	}
	if !entityExistsByName(t, store, "printPoint", "function") {
		t.Error("expected entity 'printPoint' (function) to be created")
	}
	if !entityExistsByName(t, store, "stdio.h", "import") {
		t.Error("expected entity 'stdio.h' (import) to be created")
	}
}

// TestIndexer_RustFile verifies extraction of a Rust file: 1 struct, 1
// function and 1 use.
func TestIndexer_RustFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	rsContent := `use std::fmt;

struct Point {
    x: i32,
    y: i32,
}

fn print_point(p: Point) {
    println!("{}", format!("{},{}", p.x, p.y));
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "point.rs"), []byte(rsContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Point", "type") {
		t.Error("expected entity 'Point' (type) to be created")
	}
	if !entityExistsByName(t, store, "print_point", "function") {
		t.Error("expected entity 'print_point' (function) to be created")
	}
	if !entityExistsByName(t, store, "std::fmt", "import") {
		t.Error("expected entity 'std::fmt' (import) to be created")
	}
}

// TestIndexer_ScalaFile verifies extraction of a Scala file: trait, class,
// object, case class, type alias, concrete and abstract defs, and the three
// import forms (plain, braced selectors with a rename, wildcard).
func TestIndexer_ScalaFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	scalaContent := `package com.depop.address

import java.util.UUID
import cats.effect.{IO, Sync}
import com.depop.address.repo.{AddressRepo => Repo}
import scala.jdk.CollectionConverters._

type AddressId = String

case class Address(id: AddressId, line1: String)

trait AddressService {
  def find(id: AddressId): IO[Option[Address]]
}

class AddressServiceImpl(repo: Repo) extends AddressService {
  def find(id: AddressId): IO[Option[Address]] = repo.get(id)
}

object AddressService {
  def apply(repo: Repo): AddressService = new AddressServiceImpl(repo)
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "AddressService.scala"), []byte(scalaContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	for _, name := range []string{"Address", "AddressService", "AddressServiceImpl", "AddressId"} {
		if !entityExistsByName(t, store, name, "type") {
			t.Errorf("expected entity %q (type) to be created", name)
		}
	}
	// find appears both as an abstract function_declaration and a concrete
	// function_definition; apply is an object method.
	for _, name := range []string{"find", "apply"} {
		if !entityExistsByName(t, store, name, "function") {
			t.Errorf("expected entity %q (function) to be created", name)
		}
	}
	// Plain path, each braced selector separately (original name, not the alias),
	// and the wildcard preserved as "._".
	for _, imp := range []string{
		"java.util.UUID",
		"cats.effect.IO",
		"cats.effect.Sync",
		"com.depop.address.repo.AddressRepo",
		"scala.jdk.CollectionConverters._",
	} {
		if !entityExistsByName(t, store, imp, "import") {
			t.Errorf("expected entity %q (import) to be created", imp)
		}
	}
	// The rename target must not leak in as a dependency.
	if entityExistsByName(t, store, "com.depop.address.repo.Repo", "import") {
		t.Error("alias 'Repo' was recorded as an import path; expected the original name")
	}
}

// TestIndexer_SwiftFile verifies extraction of a Swift file: 1 class, 1
// function and 1 import.
func TestIndexer_SwiftFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	swiftContent := `import Foundation

class Greeter {
    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "Greeter.swift"), []byte(swiftContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Greeter", "type") {
		t.Error("expected entity 'Greeter' (type) to be created")
	}
	if !entityExistsByName(t, store, "greet", "function") {
		t.Error("expected entity 'greet' (function) to be created")
	}
	if !entityExistsByName(t, store, "Foundation", "import") {
		t.Error("expected entity 'Foundation' (import) to be created")
	}
}

// TestIndexer_RubyFile verifies extraction of a Ruby file: 1 class, 1 method
// and 1 require.
func TestIndexer_RubyFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	rbContent := `require 'json'

class Greeter
  def greet(name)
    "Hello, #{name}"
  end
end
`
	if err := os.WriteFile(filepath.Join(srcDir, "greeter.rb"), []byte(rbContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Greeter", "type") {
		t.Error("expected entity 'Greeter' (type) to be created")
	}
	if !entityExistsByName(t, store, "greet", "function") {
		t.Error("expected entity 'greet' (function) to be created")
	}
	if !entityExistsByName(t, store, "json", "import") {
		t.Error("expected entity 'json' (import) to be created")
	}
}

// TestIndexer_CSharpFile verifies extraction of a C# file: 1 class, 1 method
// and 1 using directive.
func TestIndexer_CSharpFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	csContent := `using System;

public class Greeter
{
    public string Greet(string name)
    {
        return "Hello, " + name;
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "Greeter.cs"), []byte(csContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "Greeter", "type") {
		t.Error("expected entity 'Greeter' (type) to be created")
	}
	if !entityExistsByName(t, store, "Greet", "function") {
		t.Error("expected entity 'Greet' (function) to be created")
	}
	if !entityExistsByName(t, store, "System", "import") {
		t.Error("expected entity 'System' (import) to be created")
	}
}

// TestIndexer_JSFile_TreeSitter verifies that the tree-sitter JS parser
// extracts functions, classes and imports (upgrading from imports-only).
func TestIndexer_JSFile_TreeSitter(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	jsContent := `import React from 'react';

export function MyComponent() {
    return null;
}

export class MyClass {
    render() {
        return null;
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "component.js"), []byte(jsContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "MyComponent", "function") {
		t.Error("expected entity 'MyComponent' (function) to be created")
	}
	if !entityExistsByName(t, store, "MyClass", "type") {
		t.Error("expected entity 'MyClass' (type) to be created")
	}
	if !entityExistsByName(t, store, "react", "import") {
		t.Error("expected entity 'react' (import) to be created")
	}
}

// TestIndexer_TSFile verifies extraction of a TypeScript file.
func TestIndexer_TSFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tsContent := `import { EventEmitter } from 'events';

export interface Shape {
    area(): number;
}

export function createShape(): Shape {
    return { area: () => 0 };
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "shapes.ts"), []byte(tsContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, dbPath)

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated < 1 {
		t.Errorf("expected at least 1 entity, got %d", stats.EntitiesCreated)
	}

	if !entityExistsByName(t, store, "createShape", "function") {
		t.Error("expected entity 'createShape' (function) to be created")
	}
	if !entityExistsByName(t, store, "events", "import") {
		t.Error("expected entity 'events' (import) to be created")
	}
}

// TestIndexer_ScalaCalls verifies CALLS edges are attributed to the enclosing
// function, resolve within a file, and include calls nested inside lambdas.
func TestIndexer_ScalaCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `package com.depop.calls

object Service {
  def run(x: Int): Int = {
    val a = helper(x)
    val b = xs.map(y => helper(y))
    a
  }
  def helper(x: Int): Int = x
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "Service.scala"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	// run() calls helper() directly and again inside the map lambda; both are
	// attributed to run, and dedup collapses them to one edge.
	res, err := store.Query(`MATCH (a:Entity)-[:CALLS]->(b:Entity)
		RETURN a.id, b.id ORDER BY a.id, b.id`)
	if err != nil {
		t.Fatalf("query CALLS: %v", err)
	}
	defer res.Close()

	var edges []string
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			t.Fatalf("row: %v", err)
		}
		vals, _ := row.GetAsSlice()
		edges = append(edges, fmt.Sprint(vals))
	}

	want := "[function:Service.scala:run function:Service.scala:helper]"
	found := false
	for _, e := range edges {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CALLS edge run -> helper, got %v", edges)
	}
}

// TestIndexer_KotlinCalls covers the Kotlin extractor, which cannot use the
// default "function" field. It also pins the known blind spots so the limitation
// stays documented in code: operator conventions and callable references do not
// produce CALLS edges.
func TestIndexer_KotlinCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `package com.depop.calls

class Worker {
    fun run() {
        helper(1)
        launch { work() }
        val s = left + right
        list.forEach(::handle)
    }
    fun helper(x: Int) {}
    fun work() {}
    fun handle(x: Int) {}
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "Worker.kt"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	callees := map[string]bool{}
	res, err := store.Query(`MATCH (:Entity)-[:CALLS]->(b:Entity) RETURN b.name`)
	if err != nil {
		t.Fatalf("query CALLS: %v", err)
	}
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			t.Fatalf("row: %v", err)
		}
		v, _ := row.GetValue(0)
		name, _ := v.(string)
		callees[name] = true
	}
	res.Close()

	// Captured: a direct call, and a call inside a coroutine-builder lambda.
	for _, want := range []string{"helper", "work"} {
		if !callees[want] {
			t.Errorf("expected a CALLS edge to %q, got %v", want, callees)
		}
	}
	// Not captured, by design — documented as a lower bound:
	//   left + right   → additive_expression, not a call (operator convention)
	//   ::handle       → callable_reference, never invoked syntactically
	if callees["handle"] {
		t.Error("callable reference ::handle produced a CALLS edge; " +
			"the lower-bound caveat in the docs is now wrong and should be updated")
	}
}
