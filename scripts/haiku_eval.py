"""Measure whether Claude Haiku improves on the reranker (ADR 0011). Not part of the app.

Uses the Anthropic Claude API (model id claude-haiku-5-5), not Amazon Bedrock.
The API key is read from the file named by ANTHROPIC_API_KEY_FILE so it never appears on a command line.
One call sends about 3,200 input tokens and returns about 13 output tokens: roughly 0.00033 USD
at 0.10 / 0.50 USD per million input / output tokens. A full run of both modes is 600 calls (select 200, abstain 400), about 0.20 USD.

Candidates are the 30 nearest qa_doc embeddings of ruri-v3-30m, reranked by ruri-v3-reranker-310m (the
local reranker of ADR 0012's comparison), then the top 10 go to Haiku with their full answers.

  select   Haiku picks up to 3 candidates that answer the query; reports how often the first pick is right.
  abstain  each query runs twice, with and without its correct FAQ among the candidates; reports how often
           Haiku says "no answer" when the answer is absent.

Run after `ics load` with ruri-v3-30m:
  DATABASE_URL=... ANTHROPIC_API_KEY_FILE=~/.config/anthropic/api_key \
    python -I scripts/haiku_eval.py {select|abstain} eval/paraphrases.tsv
"""
import json, os, statistics as st, sys, time

import anthropic, psycopg
from sentence_transformers import CrossEncoder, SentenceTransformer

MODE, PARAPHRASES = sys.argv[1], sys.argv[2]
con = psycopg.connect(os.environ["DATABASE_URL"])
emb = SentenceTransformer("cl-nagoya/ruri-v3-30m")
rr = CrossEncoder("cl-nagoya/ruri-v3-reranker-310m", max_length=512)
client = anthropic.Anthropic(api_key=open(os.path.expanduser(os.environ["ANTHROPIC_API_KEY_FILE"])).read().strip())
docs = {i: (q, a) for i, q, a in con.execute("SELECT faq_id, question, answer FROM faqs")}

def candidates(q, drop=None):
    e = str(list(map(float, emb.encode(["検索クエリ: " + q], normalize_embeddings=True)[0])))
    ids = [r[0] for r in con.execute(
        "SELECT faq_id FROM embeddings WHERE model_id = 'ruri-v3-30m' AND field = 'qa_doc' "
        "ORDER BY embedding::vector(256) <=> %s::vector(256) LIMIT 31", (e,)) if r[0] != drop][:30]
    s = rr.predict([(q, docs[i][0] + "\n" + docs[i][1]) for i in ids])
    ranked = sorted(zip(s, ids), key=lambda x: -x[0])[:10]
    return [i for _, i in ranked]

SYSTEM = ("あなたは社内ヘルプデスクの仕分け担当です。利用者の問い合わせと、検索で見つかったよくある質問の候補が渡されます。"
          "まず、候補の中に問い合わせへそのまま答えている回答があるかを判定してください。"
          "話題が近いだけで、問われている具体的な内容（手続き、条件、期限、対象など）に答えていない候補は、答えているとみなしません。"
          "答えている候補があれば answered を true にし、その番号を最大3件、よく答えている順に ids へ入れてください。"
          "なければ answered を false、ids を空にしてください。回答文は書きません。")
SCHEMA = {"type": "object", "properties": {"answered": {"type": "boolean"}, "ids": {"type": "array", "items": {"type": "integer"}}},
          "required": ["answered", "ids"], "additionalProperties": False}

def ask(q, ids):
    """Returns (status, picked ids, usage, seconds); status is "answered", "none", or "refused"."""
    lines = [f"[{n}] 質問: {docs[i][0]} / 回答: {docs[i][1]}" for n, i in enumerate(ids)]
    t = time.time()
    r = client.messages.create(model="claude-haiku-5-5", max_tokens=256, system=SYSTEM, thinking={"type": "disabled"},
                               output_config={"effort": "low", "format": {"type": "json_schema", "schema": SCHEMA}},
                               messages=[{"role": "user", "content": "問い合わせ: " + q + "\n\n候補:\n" + "\n".join(lines)}])
    dt = time.time() - t
    if r.stop_reason == "refusal":  # a safety stop, not a judgment that no candidate answers
        return "refused", [], r.usage, dt
    o = json.loads(r.content[0].text)
    return ("answered" if o["answered"] else "none"), [ids[n] for n in o["ids"] if 0 <= n < len(ids)], r.usage, dt

# select: the correct FAQ may be among the candidates. abstain: both with it ("present") and without it ("absent").
conditions = ["select"] if MODE == "select" else ["present", "absent"]
rows = [l.rstrip("\n").split("\t") for l in open(PARAPHRASES, encoding="utf-8")]
for col, label in ((1, "colloquial"), (2, "short")):
    for cond in conditions:
        first = said_none = refused = 0
        tokens, lat = [], []
        for row in rows:
            target, q = int(row[0]), row[col]
            ids = candidates(q, drop=target if cond == "absent" else None)
            status, picked, usage, dt = ask(q, ids)
            tokens.append(usage.input_tokens); lat.append(dt)
            first += bool(picked) and picked[0] == target
            said_none += status == "none"
            refused += status == "refused"
        n = len(rows); lat.sort()
        metric = f"said_no_answer={said_none / n:.2f}" if cond == "absent" else f"first_pick_correct={first / n:.2f}\tsaid_no_answer={said_none / n:.2f}"
        print(f"{MODE}\t{cond}\t{label}\tn={n}\t{metric}\trefused={refused}\tinput_tokens={st.mean(tokens):.0f}\tp50={lat[n // 2]:.2f}s", flush=True)
