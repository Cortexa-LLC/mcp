package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIndexer_AsmFile_SCMASM verifies SCMASM dialect parsing:
// dot-prefix directives (.EQ, .MA/.EM), column-1 labels, `;` comments,
// `*` full-line comments.
func TestIndexer_AsmFile_SCMASM(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `* SCMASM dialect test
        .OP 65C02

* Constants
ZP.TEMP     .EQ $80
ZP.PTR      .EQ $82

* Entry point
        .OR $2000
START   LDA #$00
        STA ZP.TEMP
        RTS

* Macro definition
        .MA CLRMEM
        LDA #$00
        STA ]1
        .EM

* Include
        .IN lib/utils
`

	if err := os.WriteFile(filepath.Join(srcDir, "main.S"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Constants via .EQ
	if !entityExistsByName(t, store, "ZP.TEMP", "type") {
		t.Error("expected 'ZP.TEMP' (type) to be created")
	}
	if !entityExistsByName(t, store, "ZP.PTR", "type") {
		t.Error("expected 'ZP.PTR' (type) to be created")
	}

	// Entry point label
	if !entityExistsByName(t, store, "START", "function") {
		t.Error("expected 'START' (function) to be created")
	}

	// Macro definition (name from operand of .MA)
	if !entityExistsByName(t, store, "CLRMEM", "function") {
		t.Error("expected 'CLRMEM' (function) macro to be created")
	}

	// Include / import
	if !entityExistsByName(t, store, "lib/utils", "import") {
		t.Error("expected 'lib/utils' (import) to be created")
	}
}

// TestIndexer_AsmFile_Merlin verifies Merlin dialect parsing:
// column-1 labels (no colon), EQU and `=` constants, MAC/<<< macros,
// PUT includes, `*` full-line comments.
func TestIndexer_AsmFile_Merlin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `* Merlin dialect test
         org   $8000

SCREEN   equ   $0400
COUNTER  =     $10

* Macro
CLRSCR   MAC
         LDA   #$00
         <<<

* Entry point
Start    lda   #$00
         sta   COUNTER

Loop     inx
         bne   Loop

Done     rts

* Include
         PUT   lib/screen
`

	if err := os.WriteFile(filepath.Join(srcDir, "demo.asm"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Constants (EQU and =)
	if !entityExistsByName(t, store, "SCREEN", "type") {
		t.Error("expected 'SCREEN' (type) to be created")
	}
	if !entityExistsByName(t, store, "COUNTER", "type") {
		t.Error("expected 'COUNTER' (type) to be created")
	}

	// Macro (label=CLRSCR, opcode=MAC)
	if !entityExistsByName(t, store, "CLRSCR", "function") {
		t.Error("expected 'CLRSCR' (function) macro to be created")
	}

	// Entry point labels
	if !entityExistsByName(t, store, "Start", "function") {
		t.Error("expected 'Start' (function) to be created")
	}
	if !entityExistsByName(t, store, "Done", "function") {
		t.Error("expected 'Done' (function) to be created")
	}

	// Loop is 4 chars → significant
	if !entityExistsByName(t, store, "Loop", "function") {
		t.Error("expected 'Loop' (function) to be created")
	}

	// Include (PUT)
	if !entityExistsByName(t, store, "lib/screen", "import") {
		t.Error("expected 'lib/screen' (import) to be created")
	}
}

// TestIndexer_AsmFile_Z80 verifies Z80/EDTASM dialect parsing:
// colon-suffixed labels, EQU constants, `;` comments, INCLUDE directives.
func TestIndexer_AsmFile_Z80(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `; Z80 / EDTASM dialect test

BDOS    EQU   5
WRMSG   EQU   9

        ORG   $100

START:  LD    DE,MESSAGE
        LD    C,WRMSG
        CALL  BDOS
        RET

MESSAGE:
        DB    'Hello, World!',13,10,'$'

        INCLUDE "cpm_defs.asm"

        END   START
`

	if err := os.WriteFile(filepath.Join(srcDir, "hello.asm"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Constants (EQU)
	if !entityExistsByName(t, store, "BDOS", "type") {
		t.Error("expected 'BDOS' (type) to be created")
	}
	if !entityExistsByName(t, store, "WRMSG", "type") {
		t.Error("expected 'WRMSG' (type) to be created")
	}

	// Labels with colon (colon stripped)
	if !entityExistsByName(t, store, "START", "function") {
		t.Error("expected 'START' (function) to be created")
	}
	if !entityExistsByName(t, store, "MESSAGE", "function") {
		t.Error("expected 'MESSAGE' (function) to be created")
	}

	// Include
	if !entityExistsByName(t, store, "cpm_defs.asm", "import") {
		t.Error("expected 'cpm_defs.asm' (import) to be created")
	}
}

// TestIndexer_AsmFile_FLEX verifies FLEX/Motorola macro syntax:
// MACRO/ENDM pairs, label-position macro names.
func TestIndexer_AsmFile_FLEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `; FLEX/Motorola dialect test

MOVB    MACRO   SRC,DEST
        LDA     SRC
        STA     DEST
        ENDM

ADDM    MACRO   VAL1,VAL2,RESULT
        LDA     VAL1
        ADDA    VAL2
        STA     RESULT
        ENDM

        ORG     $1000

START   MOVB    $80,$90
        ADDM    $C0,$C1,$C2

        END     START
`

	if err := os.WriteFile(filepath.Join(srcDir, "macros.asm"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Macros (label=name, opcode=MACRO)
	if !entityExistsByName(t, store, "MOVB", "function") {
		t.Error("expected 'MOVB' (function) macro to be created")
	}
	if !entityExistsByName(t, store, "ADDM", "function") {
		t.Error("expected 'ADDM' (function) macro to be created")
	}

	// Entry point label
	if !entityExistsByName(t, store, "START", "function") {
		t.Error("expected 'START' (function) to be created")
	}
}

// TestIndexer_AsmFile_Generic6502 verifies generic 6502 syntax:
// colon-suffixed labels on their own lines, lowercase directives.
func TestIndexer_AsmFile_Generic6502(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `; Generic 6502 program
  .org $8000

start:
  lda #$42
  sta $0200

loop:
  inx
  cpx #$10
  bne loop

  jmp start
  rts
`

	if err := os.WriteFile(filepath.Join(srcDir, "prog.asm"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Label-only lines (colon stripped)
	if !entityExistsByName(t, store, "start", "function") {
		t.Error("expected 'start' (function) to be created")
	}
	if !entityExistsByName(t, store, "loop", "function") {
		t.Error("expected 'loop' (function) to be created")
	}
}

// TestIndexer_AsmFile_CompoundExtension verifies that .S.txt and .asm.txt
// files (used by A2osX and similar projects) are recognised and parsed.
func TestIndexer_AsmFile_CompoundExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A2osX-style SCMASM file stored with .txt suffix
	content := `*--------------------------------------
SSCIRQ			.EQ	0
*--------------------------------------
				.INB inc/macros.i
				.INB inc/a2osx.i
*--------------------------------------
MAIN			LDA #$00
				RTS
`
	if err := os.WriteFile(filepath.Join(srcDir, "SSC.DRV.S.txt"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	if !entityExistsByName(t, store, "SSCIRQ", "type") {
		t.Error("expected 'SSCIRQ' (type) constant to be created")
	}
	if !entityExistsByName(t, store, "MAIN", "function") {
		t.Error("expected 'MAIN' (function) to be created")
	}
	if !entityExistsByName(t, store, "inc/macros.i", "import") {
		t.Error("expected 'inc/macros.i' (import) via .INB to be created")
	}
}

// TestIndexer_AsmFile_MacroBodySkipped verifies that labels defined inside
// a macro body are NOT indexed (they're local/private, not public symbols).
func TestIndexer_AsmFile_MacroBodySkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `* Test that macro-body labels are not indexed

OUTERM  MAC
INNER_LABEL
        LDA #0
        <<<

OUTER   LDA #1
`

	if err := os.WriteFile(filepath.Join(srcDir, "macrobody.asm"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	// Macro definition itself should be indexed
	if !entityExistsByName(t, store, "OUTERM", "function") {
		t.Error("expected 'OUTERM' (function) to be created")
	}

	// Label inside macro body must NOT be indexed
	if entityExistsByName(t, store, "INNER_LABEL", "") {
		t.Error("'INNER_LABEL' inside macro body must NOT be indexed")
	}

	// Label after macro must be indexed
	if !entityExistsByName(t, store, "OUTER", "function") {
		t.Error("expected 'OUTER' (function) after macro to be created")
	}
}
