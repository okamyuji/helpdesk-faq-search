CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_bigm;

CREATE TABLE IF NOT EXISTS faqs (
  faq_id   integer PRIMARY KEY,
  question text NOT NULL,
  answer   text NOT NULL,
  agency   text NOT NULL,
  url      text NOT NULL,
  qa_norm  text NOT NULL
);
CREATE INDEX IF NOT EXISTS faqs_qa_bigm ON faqs USING gin (qa_norm gin_bigm_ops);

-- One column holds every model's vectors; HNSW indexes are partial per model and field (see Migrate).
CREATE TABLE IF NOT EXISTS embeddings (
  faq_id    integer NOT NULL REFERENCES faqs ON DELETE CASCADE,
  field     text NOT NULL CHECK (field IN ('qa_doc', 'qa_plain', 'answer_doc')),
  model_id  text NOT NULL,
  embedding vector NOT NULL,
  PRIMARY KEY (faq_id, field, model_id)
);
