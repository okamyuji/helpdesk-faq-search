# PostgreSQL with the same extension versions that RDS for PostgreSQL 18 offers.
FROM postgres:18.6-bookworm
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends build-essential git ca-certificates postgresql-server-dev-18; \
    git clone --depth 1 --branch v0.8.2 https://github.com/pgvector/pgvector /tmp/pgvector; \
    make -C /tmp/pgvector OPTFLAGS=""; \
    make -C /tmp/pgvector install; \
    git clone --depth 1 --branch v1.2-20250903 https://github.com/pgbigm/pg_bigm /tmp/pg_bigm; \
    make -C /tmp/pg_bigm USE_PGXS=1; \
    make -C /tmp/pg_bigm USE_PGXS=1 install; \
    apt-get purge -y --auto-remove build-essential git postgresql-server-dev-18; \
    rm -rf /var/lib/apt/lists/* /tmp/pgvector /tmp/pg_bigm
