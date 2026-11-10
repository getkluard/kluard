package registry

import "testing"

func TestStatusPages(t *testing.T) {
	s, _ := newReg(t)
	w, err := s.CreateWeb("acme", WebIn{Host: "web1", URL: "https://shop.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateWeb("globex", WebIn{Host: "web9", URL: "https://globex.example/"})
	if err != nil {
		t.Fatal(err)
	}
	in := StatusPageIn{Title: "Acme cloud", Items: []StatusItem{{Kind: "web", Ref: w.ID, Label: "Our shop"}}}
	p, err := s.CreateStatusPage("acme", in)
	if err != nil || !ValidStatusPageID(p.ID) {
		t.Fatalf("%+v %v", p, err)
	}
	if got, err := s.GetStatusPage(p.ID); err != nil || got.Title != "Acme cloud" || got.Items[0].Label != "Our shop" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.GetStatusPage("sp000000000000000000000000"); err == nil {
		t.Fatal("unknown id")
	}
	if _, err := s.GetStatusPage("../etc"); err == nil {
		t.Fatal("bad id")
	}
	if len(s.ListStatusPages("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	// a page may only show things its own tenant owns
	if _, err := s.CreateStatusPage("acme", StatusPageIn{Title: "x", Items: []StatusItem{{Kind: "web", Ref: other.ID, Label: "theirs"}}}); err == nil {
		t.Fatal("another tenant's website on the page")
	}
	if _, err := s.UpdateStatusPage("globex", p.ID, in); err == nil {
		t.Fatal("another tenant changed it")
	}
	if err := s.DeleteStatusPage("globex", p.ID); err == nil {
		t.Fatal("another tenant removed it")
	}
	for name, bad := range map[string]StatusPageIn{
		"no title":   {Items: in.Items},
		"no items":   {Title: "x"},
		"no label":   {Title: "x", Items: []StatusItem{{Kind: "web", Ref: w.ID}}},
		"bad kind":   {Title: "x", Items: []StatusItem{{Kind: "host", Ref: w.ID, Label: "a"}}},
		"twice":      {Title: "x", Items: []StatusItem{{Kind: "web", Ref: w.ID, Label: "a"}, {Kind: "web", Ref: w.ID, Label: "b"}}},
		"long text":  {Title: "x", Message: string(make([]byte, 301)), Items: in.Items},
		"multi-line": {Title: "a\nb", Items: in.Items},
	} {
		if _, err := s.CreateStatusPage("acme", bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	in.Title = "Acme status"
	if got, err := s.UpdateStatusPage("acme", p.ID, in); err != nil || got.Title != "Acme status" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.DeleteStatusPage("acme", p.ID); err != nil || len(s.ListStatusPages("acme")) != 0 {
		t.Fatal("delete")
	}
}
