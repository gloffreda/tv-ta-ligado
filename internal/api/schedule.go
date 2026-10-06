package api

import (
	"net/http"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
)

type blockView struct {
	Name  string   `json:"name"`
	Every string   `json:"every"`
	Lead  string   `json:"lead"`
	Cast  []string `json:"cast"`
	Scene string   `json:"scene,omitempty"`
	Data  string   `json:"data,omitempty"`
}

type blackoutView struct {
	Name   string    `json:"name"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Active bool      `json:"active"`
}

// schedule: grade de hoje e de amanhã (Brasília), blocos, modo público e
// janelas de bloqueio. O selo da página sai daqui: preview → EM TESTE,
// live → AO VIVO, dentro de bloqueio → REPRISE.
func (s *Server) schedule(w http.ResponseWriter, r *http.Request) {
	sched, err := config.LoadSchedule(s.ConfigDir)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "schedule.yaml: " + err.Error()})
		return
	}
	bo, _ := config.LoadBlackout(s.ConfigDir)
	now := s.now()
	loc := s.Loc
	if loc == nil {
		loc = time.UTC
	}
	var slots, weather []config.Slot
	for d := 0; d < 2; d++ {
		p, wt := sched.Programs.Day(now.In(loc).AddDate(0, 0, d), loc)
		slots = append(slots, p...)
		weather = append(weather, wt...)
	}
	var blocks []blockView
	for _, b := range sched.Blocks {
		blocks = append(blocks, blockView{Name: b.Name, Every: b.Every.Duration.String(), Lead: b.Lead, Cast: b.CastOrDefault(), Scene: b.Scene, Data: b.Data})
	}
	var bos []blackoutView
	active := false
	for _, win := range bo.Windows {
		a := !now.Before(win.Start) && now.Before(win.End)
		active = active || a
		bos = append(bos, blackoutView{Name: win.Name, Start: win.Start.UTC(), End: win.End.UTC(), Active: a})
	}
	type personaView struct {
		Name         string                `json:"name"`
		Role         string                `json:"role"`
		Catchphrases []string              `json:"catchphrases"`
		Rigs         map[string]config.Rig `json:"rigs"`
	}
	personas := map[string]personaView{}
	if ps, err := config.LoadPersonas(s.ConfigDir); err == nil {
		for id, p := range ps {
			personas[id] = personaView{Name: p.Name, Role: p.Role, Catchphrases: p.Catchphrases, Rigs: p.Rigs}
		}
	}
	cur, _ := sched.Programs.At(now, loc)
	badge := "EM TESTE"
	switch {
	case active:
		badge = "REPRISE"
	case s.PublicMode == "live":
		badge = "AO VIVO"
	}
	writeJSON(w, 200, map[string]any{
		"server_time": now, "timezone": loc.String(), "public_mode": s.PublicMode, "badge": badge,
		"now": cur, "programs": slots, "weather": weather, "plantao": sched.Programs.Plantao,
		"blocks": blocks, "blackout": bos, "blackout_active": active, "personas": personas,
	})
}

// Badge: a regra do selo (também implementada no cliente).
func Badge(publicMode string, blackoutActive bool) string {
	switch {
	case blackoutActive:
		return "REPRISE"
	case publicMode == "live":
		return "AO VIVO"
	}
	return "EM TESTE"
}
