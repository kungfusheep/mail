package compose

import "testing"

func TestTemplateSelection(t *testing.T) {
	r := NewTemplateRegistry()

	// Test that empty string returns "clean"
	tmpl := r.GetHeadingTemplate("")
	if tmpl == nil {
		t.Fatal("GetHeadingTemplate(\"\") returned nil")
	}
	if tmpl.Name != "clean" {
		t.Errorf("GetHeadingTemplate(\"\") = %q, want \"clean\"", tmpl.Name)
	}

	// Test that "banner" returns "banner"
	tmpl = r.GetHeadingTemplate("banner")
	if tmpl == nil {
		t.Fatal("GetHeadingTemplate(\"banner\") returned nil")
	}
	if tmpl.Name != "banner" {
		t.Errorf("GetHeadingTemplate(\"banner\") = %q, want \"banner\"", tmpl.Name)
	}

	// Test that unknown returns "clean"
	tmpl = r.GetHeadingTemplate("nonexistent")
	if tmpl == nil {
		t.Fatal("GetHeadingTemplate(\"nonexistent\") returned nil")
	}
	if tmpl.Name != "clean" {
		t.Errorf("GetHeadingTemplate(\"nonexistent\") = %q, want \"clean\"", tmpl.Name)
	}
}

func TestCategorySystem(t *testing.T) {
	r := NewTemplateRegistry()

	// test all categories exist
	categories := []string{"headings", "quotes", "dividers", "lists", "code", "callouts", "frontmatter"}
	for _, cat := range categories {
		if r.GetCategory(cat) == nil {
			t.Errorf("category %q not found", cat)
		}
	}
}

func TestNewTemplates(t *testing.T) {
	r := NewTemplateRegistry()

	// headings
	headings := []string{"clean", "banner", "underlined", "edge", "typewriter", "academic", "minimal", "boxed"}
	for _, name := range headings {
		if tmpl := r.GetHeadingTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("heading template %q not found", name)
		}
	}

	// quotes
	quotes := []string{"bar", "indent", "marks", "typewriter"}
	for _, name := range quotes {
		if tmpl := r.GetQuoteTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("quote template %q not found", name)
		}
	}

	// dividers
	dividers := []string{"line", "double", "dots", "stars", "ornate", "typewriter"}
	for _, name := range dividers {
		if tmpl := r.GetDividerTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("divider template %q not found", name)
		}
	}

	// lists
	lists := []string{"bullet", "dash", "arrow", "checkbox"}
	for _, name := range lists {
		if tmpl := r.GetListTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("list template %q not found", name)
		}
	}

	// code
	code := []string{"minimal", "boxed", "terminal"}
	for _, name := range code {
		if tmpl := r.GetCodeTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("code template %q not found", name)
		}
	}

	// callouts
	callouts := []string{"minimal", "boxed", "bracket"}
	for _, name := range callouts {
		if tmpl := r.GetCalloutTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("callout template %q not found", name)
		}
	}

	// frontmatter
	fm := []string{"minimal", "table", "boxed", "hidden"}
	for _, name := range fm {
		if tmpl := r.GetFrontmatterTemplate(name); tmpl == nil || tmpl.Name != name {
			t.Errorf("frontmatter template %q not found", name)
		}
	}
}

func TestStyleBundles(t *testing.T) {
	r := NewTemplateRegistry()

	bundles := []string{"typewriter", "minimal", "academic", "creative"}
	for _, name := range bundles {
		bundle := r.GetBundle(name)
		if bundle == nil {
			t.Errorf("bundle %q not found", name)
			continue
		}
		if bundle.Name != name {
			t.Errorf("bundle.Name = %q, want %q", bundle.Name, name)
		}
		if len(bundle.Templates) == 0 {
			t.Errorf("bundle %q has no templates", name)
		}
	}
}

func TestBundleTemplateMapping(t *testing.T) {
	r := NewTemplateRegistry()

	// verify typewriter bundle maps to expected templates
	bundle := r.GetBundle("typewriter")
	if bundle == nil {
		t.Fatal("typewriter bundle not found")
	}

	expected := map[string]string{
		"headings":    "typewriter",
		"quotes":      "typewriter",
		"dividers":    "typewriter",
		"lists":       "dash",
		"code":        "minimal",
		"callouts":    "bracket",
		"frontmatter": "minimal",
	}

	for cat, tmpl := range expected {
		if bundle.Templates[cat] != tmpl {
			t.Errorf("typewriter bundle %q = %q, want %q", cat, bundle.Templates[cat], tmpl)
		}
	}
}

func TestCategoryExists(t *testing.T) {
	r := NewTemplateRegistry()
	cat := r.GetCategory("headings")

	if !cat.Exists("clean") {
		t.Error("clean should exist in headings")
	}
	if cat.Exists("nonexistent") {
		t.Error("nonexistent should not exist")
	}
}
