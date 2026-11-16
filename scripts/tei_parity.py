"""Compare TEI with sentence-transformers: embeddings for the same prefixes, or reranker orderings."""
import sys

import numpy as np
import requests
from sentence_transformers import CrossEncoder, SentenceTransformer

TEXTS = [
    "今日は晴れていて散歩に出かけた。", "駅前の本屋で新しい小説を買った。", "会議の資料を明日までに用意する。",
    "冷蔵庫の牛乳が切れそうだ。", "週末は家族で公園に行く予定だ。", "新しいノートパソコンの設定を終えた。",
    "雨の日は電車がよく遅れる。", "料理教室でパンの焼き方を習った。", "図書館で静かに勉強した。",
    "旅行の写真をアルバムに整理した。", "朝のジョギングを三日続けている。", "庭の花に水をやった。",
    "古い自転車のタイヤを交換した。", "友人と映画を見に行った。", "部屋の掃除を午前中に済ませた。",
    "博物館で恐竜の化石を見た。", "コーヒーを淹れて一息ついた。", "地図アプリで道を調べた。",
    "子どもと一緒に折り紙を折った。", "夜空に流れ星が見えた。",
]
PREFIXES = ["", "検索クエリ: ", "検索文書: "]
QUERIES = ["週末の予定", "本を買った場所", "朝の運動", "家の片付け", "空に見えたもの"]


def rerank_parity(model: str, url: str) -> int:
    ce = CrossEncoder(f"cl-nagoya/{model}", device="cpu")
    mismatches = 0
    for q in QUERIES:
        texts = TEXTS[:10]
        ref = ce.predict([(q, t) for t in texts])
        ref_order = [int(i) for i in np.argsort(-ref)]
        res = requests.post(f"{url}/rerank", json={"query": q, "texts": texts}, timeout=120)
        res.raise_for_status()
        got = res.json()
        got_order = [r["index"] for r in got]
        diff = max(abs(float(ref[r["index"]]) - r["score"]) for r in got)
        print(f"{q} order_match={got_order == ref_order} max_score_diff={diff:.5f}")
        mismatches += got_order != ref_order
    return 0 if mismatches == 0 else 1


def main() -> int:
    model, url = sys.argv[1], sys.argv[2]
    if "reranker" in model:
        return rerank_parity(model, url)
    st = SentenceTransformer(f"cl-nagoya/{model}", device="cpu")
    worst = 1.0
    for prefix in PREFIXES:
        inputs = [prefix + t for t in TEXTS]
        ref = st.encode(inputs, normalize_embeddings=True)
        res = requests.post(f"{url}/embed", json={"inputs": inputs, "normalize": True}, timeout=120)
        res.raise_for_status()
        got = np.asarray(res.json(), dtype=np.float32)
        worst = min(worst, float(np.min(np.sum(ref * got, axis=1))))
    print(f"min cosine {worst:.5f}")
    return 0 if worst >= 0.99 else 1


if __name__ == "__main__":
    sys.exit(main())
