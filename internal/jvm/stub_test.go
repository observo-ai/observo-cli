package jvm

import (
	"strings"
	"testing"
)

func sampleClass() StubClass {
	return StubClass{
		Package:   "api.pd",
		ClassName: "PdWalletTest",
		Feature:   "PD Wallet",
		Cases: []StubCase{
			{Code: "PD-101", Name: "trader creates a wallet",
				Description: "Wallet is created on first login", Layer: "API",
				Steps: []string{"POST /wallet", "expect 201"}},
			{Code: "PD-102", Name: "trader deposits funds", Layer: "API"},
		},
	}
}

func TestRenderKotlin_TestNG(t *testing.T) {
	out, err := RenderKotlin(sampleClass(), FrameworkTestNG)
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		"package api.pd",
		"import org.testng.annotations.Test",
		"import io.qameta.allure.Feature",
		"import io.qameta.allure.Description",
		`@Feature("PD Wallet")`,
		"class PdWalletTest {",
		`@Test(groups = ["observo:PD-101"])`,
		`@Description("Wallet is created on first login")`,
		"fun `trader creates a wallet`() {",
		"// 1. POST /wallet",
		`TODO("implement PD-101")`,
		"· layer: API",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("TestNG output missing %q\n---\n%s", w, out)
		}
	}
	// JUnit5-only import must not leak in.
	if strings.Contains(out, "org.junit.jupiter") {
		t.Errorf("TestNG output leaked JUnit import:\n%s", out)
	}
	assertBalancedBraces(t, out)
}

func TestRenderKotlin_JUnit5(t *testing.T) {
	out, err := RenderKotlin(sampleClass(), FrameworkJUnit5)
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		"import org.junit.jupiter.api.Tag",
		"import org.junit.jupiter.api.Test",
		"@Test\n    @Tag(\"observo:PD-101\")",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("JUnit5 output missing %q\n---\n%s", w, out)
		}
	}
	if strings.Contains(out, "groups = [") {
		t.Errorf("JUnit5 output must not use TestNG groups:\n%s", out)
	}
	if strings.Contains(out, "org.testng") {
		t.Errorf("JUnit5 output leaked TestNG import:\n%s", out)
	}
	assertBalancedBraces(t, out)
}

func TestRenderKotlin_UnsupportedFramework(t *testing.T) {
	if _, err := RenderKotlin(sampleClass(), "spock"); err == nil {
		t.Error("expected error for unsupported framework")
	}
}

func TestRenderKotlin_NoDescriptionNoImport(t *testing.T) {
	cls := StubClass{
		ClassName: "T",
		Cases:     []StubCase{{Code: "PD-9", Name: "x"}},
	}
	out, err := RenderKotlin(cls, FrameworkTestNG)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Description") {
		t.Errorf("no-description class must not import/emit @Description:\n%s", out)
	}
	if strings.Contains(out, "@Feature") {
		t.Errorf("empty feature must not emit @Feature:\n%s", out)
	}
	if strings.Contains(out, "package ") {
		t.Errorf("empty package must not emit a package line:\n%s", out)
	}
}

func TestUniqueFunName_DisambiguatesCollisions(t *testing.T) {
	seen := map[string]bool{}
	a := uniqueFunName(StubCase{Code: "PD-1", Name: "same name"}, seen)
	b := uniqueFunName(StubCase{Code: "PD-2", Name: "same name"}, seen)
	if a == b {
		t.Fatalf("collision not disambiguated: both %q", a)
	}
	if !strings.Contains(b, "PD-2") {
		t.Errorf("second name should carry its code: %q", b)
	}
}

func TestBacktickName_SanitizesIllegalChars(t *testing.T) {
	// Dots/brackets/backticks are illegal in a Kotlin backtick identifier.
	got := backtickName("api.Foo[bar] `baz`")
	for _, bad := range []string{".", "[", "]", "`"} {
		if strings.Contains(got, bad) {
			t.Errorf("backtickName kept illegal %q: %q", bad, got)
		}
	}
}

func TestKotlinString_Escapes(t *testing.T) {
	got := kotlinString(`he said "hi" $x \ end`)
	for _, want := range []string{`\"hi\"`, `\$x`, `\\`} {
		if !strings.Contains(got, want) {
			t.Errorf("kotlinString missing escape %q in %q", want, got)
		}
	}
}

func TestClassNameFromFeature(t *testing.T) {
	cases := map[string]string{
		"PD Wallet":     "PDWalletTest", // acronym casing preserved
		"deposits-flow": "DepositsFlowTest",
		"":              "ObservoStubTest",
		"!!!":           "ObservoStubTest",
		"123 numbers":   "_123NumbersTest",
		"AlreadyTest":   "AlreadyTest",
	}
	for in, want := range cases {
		if got := ClassNameFromFeature(in); got != want {
			t.Errorf("ClassNameFromFeature(%q) = %q, want %q", in, got, want)
		}
	}
}

// assertBalancedBraces is a cheap structural sanity check that the emitted
// Kotlin has matched { } (guards against a template regression producing
// syntactically broken output).
func assertBalancedBraces(t *testing.T, src string) {
	t.Helper()
	depth := 0
	for _, r := range src {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				t.Fatalf("unbalanced braces (extra }):\n%s", src)
			}
		}
	}
	if depth != 0 {
		t.Fatalf("unbalanced braces (depth %d):\n%s", depth, src)
	}
}
