// The executable schema is generated from Go in schemas/edition.schema.json.
export interface Source { id: string; name: string; title: string; url: string; published_at: string | null; discovered_at: string; discussion_url: string | null }
export interface Article { id: string; section: string; headline: string; summary: string; paragraphs: string[]; implication: string; source_ids: string[] }
export interface Lesson { id: string; title: string; paragraphs: string[]; example: string; takeaway: string; article_ids: string[]; source_ids: string[] }
export interface Question { id: string; lesson_id: string; prompt: string; options: string[]; answer: number; explanation: string }
export interface Segment { id: string; title: string; text: string; article_ids: string[] }
export interface TranscriptCue { segment_id: string; text: string; start_seconds: number; end_seconds: number }
export interface Edition {
  schema_version: string; date: string; timezone: string; revision: number; demo: boolean; generated_at: string;
  coverage: { from: string; until: string; status: string; unavailable_sources: string[]; candidate_count: number; evidence_count: number };
  content: { title: string; introduction: string; articles: Article[]; lessons: Lesson[]; quiz: Question[]; segments: Segment[]; artwork_brief: string };
  sources: Source[];
  radio: { status: string; audio_url: string | null; duration_seconds: number | null; script_hash: string; cues?: TranscriptCue[] };
  artwork: { status: string; url: string | null; alt: string };
}
