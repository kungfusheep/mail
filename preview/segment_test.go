package preview

import "testing"

func TestSegmentText_PreservesQuotedReply(t *testing.T) {
	body := "Sounds good.\n\nOn Tue, Bob wrote:\n> earlier point"

	got := SegmentText(body)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2: %+v", len(got), got)
	}
	if got[0].Kind != SegmentMain || got[0].Text != "Sounds good." {
		t.Fatalf("main segment = %+v", got[0])
	}
	if got[1].Kind != SegmentQuote || got[1].Text != "On Tue, Bob wrote:\n> earlier point" {
		t.Fatalf("quote segment = %+v", got[1])
	}
}

func TestSegmentText_ClassifiesSignatureAndFooter(t *testing.T) {
	body := "Please see attached.\n-- \nAlex\n\nPrivacy policy: example.com/privacy"

	got := SegmentText(body)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2: %+v", len(got), got)
	}
	if got[0].Kind != SegmentMain {
		t.Fatalf("first kind = %v, want main", got[0].Kind)
	}
	if got[1].Kind != SegmentSignature {
		t.Fatalf("second kind = %v, want signature", got[1].Kind)
	}
}

func TestSegmentText_ClassifiesFooterWithoutSignature(t *testing.T) {
	body := "Your vouchers are ready.\n\nPrivacy policy >\nTerms & conditions >"

	got := SegmentText(body)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2: %+v", len(got), got)
	}
	if got[1].Kind != SegmentFooter {
		t.Fatalf("second kind = %v, want footer", got[1].Kind)
	}
}

func TestSegmentText_ClassifiesLegalFooterTerms(t *testing.T) {
	body := "Your order is on its way.\n\nYou are receiving this email because you bought something from us.\nCopyright 2026 Example Ltd. All rights reserved.\nCompany number 123456."

	got := SegmentText(body)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2: %+v", len(got), got)
	}
	if got[0].Kind != SegmentMain {
		t.Fatalf("first kind = %v, want main", got[0].Kind)
	}
	if got[1].Kind != SegmentFooter {
		t.Fatalf("second kind = %v, want footer", got[1].Kind)
	}
}
