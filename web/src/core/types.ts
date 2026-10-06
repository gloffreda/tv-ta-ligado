// Formatos reais da API /v1 (ver internal/api).

export type Viseme = { start_ms: number; end_ms: number; shape: string };

export type Source = { name: string; url: string; title?: string };

export type Line = {
  seq: number;
  line_id?: number;
  speaker: string;
  type: "fact" | "banter" | string;
  text: string;
  spoken_text: string;
  audio_hash: string;
  offset_ms: number;
  duration_ms: number;
  visemes?: Viseme[] | null;
  sources?: Source[];
  audio_url: string;
  starts_at: string;
  ends_at: string;
};

export type Item = {
  id: number;
  kind: "segment" | "data" | "replay" | "bumper" | "silence" | string;
  segment_id?: number;
  block?: string;
  starts_at: string;
  ends_at: string;
  status: string;
  lines: Line[] | null;
};

export type NowResp = {
  server_time: string;
  item: Item | null;
  line: Line | null;
  position_ms: number;
  item_position_ms: number;
  next_line?: Line | null;
  next_line_in_ms?: number;
};

export type Program = {
  start: string;
  end?: string;
  name: string;
  scene: string;
  cast: string[];
  about?: string;
  starts_at: string;
  ends_at: string;
  kind: "program" | "weather";
};

export type Rig = { sprite?: string; seat?: string; palette?: Record<string, string> };

export type PersonaInfo = { name: string; role: string; catchphrases: string[]; rigs: Record<string, Rig> };

export type BlackoutWindow = { name: string; start: string; end: string; active: boolean };

export type Schedule = {
  server_time: string;
  timezone: string;
  public_mode: "preview" | "live" | string;
  badge: string;
  now: Program;
  programs: Program[];
  weather: Program[];
  plantao: Program;
  blocks: { name: string; every: string; lead: string; cast: string[]; scene?: string; data?: string }[];
  blackout: BlackoutWindow[] | null;
  blackout_active: boolean;
  personas: Record<string, PersonaInfo>;
};

export type SessionInfo = {
  id: number;
  status: "active" | "ended";
  started_at: string;
  deadline: string;
  max_usd: number;
  ended_at?: string;
  end_reason?: string;
  spent_usd: number;
  first_line_at?: string;
};

export type SessionState = {
  active: boolean;
  on_air: boolean;
  last?: SessionInfo;
  max_min: number;
  max_usd: number;
  viewers: number;
};
