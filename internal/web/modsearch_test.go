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

func TestParseWorkshopSearchNewLayout(t *testing.T) {
	page := `
<div class="Panel"><div class="grid">
  <a href="https://steamcommunity.com/sharedfiles/filedetails/?id=2964299587" class="card">
    <img src="https://images.steamusercontent.com/ugc/2036/dmg.jpg?ima=fit&amp;imw=322" alt="伤害显示(Show Damage)" loading="lazy"/>
  </a>
  <a href="https://steamcommunity.com/sharedfiles/filedetails/?id=3561785531" class="card">
    <img src="https://images.steamusercontent.com/ugc/1634/timer.jpg" alt="全局事件计时器(Global Events Timer)" loading="lazy"/>
  </a>
  <a href="http://steamcommunity.com/sharedfiles/filedetails/?id=378160973" class="card">
    <img src="https://cdn.example/3.jpg" alt="Global Positions"/>
  </a>
  <a href="https://steamcommunity.com/sharedfiles/filedetails/?id=2964299587" class="card">
    <img src="https://images.steamusercontent.com/ugc/2036/dmg.jpg" alt="伤害显示(Show Damage)"/>
  </a>
</div></div>
`
	results := parseWorkshopSearch(page)
	if len(results) != 3 {
		t.Fatalf("got %d results: %+v", len(results), results)
	}
	if results[0].ID != "2964299587" || results[0].Title != "伤害显示(Show Damage)" {
		t.Fatalf("result 0 = %+v", results[0])
	}
	if results[1].Title != "全局事件计时器(Global Events Timer)" {
		t.Fatalf("chinese title = %q", results[1].Title)
	}
	if results[0].Preview != "https://images.steamusercontent.com/ugc/2036/dmg.jpg?ima=fit&imw=322" {
		t.Fatalf("preview = %q", results[0].Preview)
	}
	if results[2].URL != "https://steamcommunity.com/sharedfiles/filedetails/?id=378160973" {
		t.Fatalf("url = %q", results[2].URL)
	}
}

func TestWorkshopItemInfoPrefersTitleDiv(t *testing.T) {
	inner := `<img src="https://cdn.example/a.jpg" alt="Alt Name"><div class="workshopItemTitle">Div &amp; Title</div>`
	title, preview := workshopItemInfo(inner)
	if title != "Div & Title" {
		t.Fatalf("title = %q", title)
	}
	if preview != "https://cdn.example/a.jpg" {
		t.Fatalf("preview = %q", preview)
	}
}

func TestWorkshopMirrorOrder(t *testing.T) {
	defer rememberMirror(0)
	rememberMirror(2)
	order := mirrorOrder()
	if len(order) != len(workshopMirrors) || order[0] != 2 || order[1] != 0 || order[2] != 1 {
		t.Fatalf("order = %v", order)
	}
	seen := map[int]bool{}
	for _, idx := range order {
		if idx < 0 || idx >= len(workshopMirrors) || seen[idx] {
			t.Fatalf("bad mirror order %v", order)
		}
		seen[idx] = true
	}
}
