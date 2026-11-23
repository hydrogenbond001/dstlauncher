package web

import "testing"

func TestParseWorkshopSearch(t *testing.T) {
	page := `
<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=378160973&searchtext=global" class="workshopItem">
  <img src="https://cdn.example/1.jpg">
  <div class="workshopItemTitle">Global Positions</div>
</a>
<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=111111111&searchtext=global" class="workshopItem">
  <img src="https://cdn.example/2.jpg">
  <div class="workshopItemTitle">Some &amp; Mod</div>
</a>
<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=378160973&searchtext=global" class="workshopItem">
  <div class="workshopItemTitle">Duplicate</div>
</a>
`
	results := parseWorkshopSearch(page)
	if len(results) != 2 {
		t.Fatalf("got %d results: %+v", len(results), results)
	}
	if results[0].ID != "378160973" || results[0].Title != "Global Positions" {
		t.Fatalf("result 0 = %+v", results[0])
	}
	if results[0].Preview != "https://cdn.example/1.jpg" {
		t.Fatalf("preview = %q", results[0].Preview)
	}
	if results[1].Title != "Some & Mod" {
		t.Fatalf("html entities not decoded: %q", results[1].Title)
	}
}
