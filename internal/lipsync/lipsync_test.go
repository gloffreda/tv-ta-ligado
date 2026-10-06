package lipsync

import "testing"

func TestParseRhubarb(t *testing.T) {
	cues, err := ParseRhubarb([]byte(`{"metadata":{"duration":0.47},"mouthCues":[{"start":0.00,"end":0.05,"value":"X"},{"start":0.05,"end":0.27,"value":"D"},{"start":0.27,"end":0.47,"value":"X"}]}`))
	if err != nil || len(cues) != 3 || cues[1] != (Cue{StartMS: 50, EndMS: 270, Shape: "D"}) {
		t.Fatalf("%+v %v", cues, err)
	}
}
