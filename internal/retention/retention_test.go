package retention

import "testing"

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		data, keep int
		ok         bool
	}{{30, 365, true}, {1, 0, true}, {3650, 36500, true}, {0, 365, false}, {3651, 0, false}, {30, -1, false}, {30, 36501, false}} {
		if err := Validate(c.data, c.keep); (err == nil) != c.ok {
			t.Errorf("Validate(%d,%d) = %v, want ok=%v", c.data, c.keep, err, c.ok)
		}
	}
}

func TestDays(t *testing.T) {
	d := New(30, 365)
	if a, b := d.Get(); a != 30 || b != 365 {
		t.Fatal(a, b)
	}
	d.Set(14, 0, true)
	a, b := d.Get()
	dd, dk, chosen := d.Default()
	if a != 14 || b != 0 || dd != 30 || dk != 365 || !chosen {
		t.Fatal(a, b, dd, dk, chosen)
	}
}
