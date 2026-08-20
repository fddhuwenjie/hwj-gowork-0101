// Package repository 提供基于 database/sql 的 SQLite 持久化实现。
//
// 所有多实体写入都通过 TxManager 在单事务内完成；SchemaDDL 在进程
// 启动时幂等执行，数据库文件可跨进程重启复用。
package repository

const SchemaDDL = `
CREATE TABLE IF NOT EXISTS suppliers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    contact    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS grade_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    grade      TEXT NOT NULL,
    version_no INTEGER NOT NULL,
    elements   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'draft',
    remark     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1,
    UNIQUE (grade, version_no)
);
CREATE INDEX IF NOT EXISTS idx_grade_rules_grade ON grade_rules (grade, status);

CREATE TABLE IF NOT EXISTS material_lots (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_no         TEXT NOT NULL UNIQUE,
    supplier_id    INTEGER NOT NULL REFERENCES suppliers(id),
    heat_no        TEXT NOT NULL,
    grade          TEXT NOT NULL,
    quantity       REAL NOT NULL,
    status         TEXT NOT NULL DEFAULT 'registered',
    initial_result TEXT NOT NULL DEFAULT '',
    retest_result  TEXT NOT NULL DEFAULT '',
    accepted_by    TEXT NOT NULL DEFAULT '',
    accepted_at    TEXT,
    received_at    TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    version        INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_lots_status ON material_lots (status, id);
CREATE INDEX IF NOT EXISTS idx_lots_supplier ON material_lots (supplier_id, status);
CREATE INDEX IF NOT EXISTS idx_lots_received ON material_lots (received_at);

CREATE TABLE IF NOT EXISTS mill_certificates (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    cert_no     TEXT NOT NULL UNIQUE,
    lot_id      INTEGER NOT NULL REFERENCES material_lots(id),
    grade       TEXT NOT NULL,
    heat_no     TEXT NOT NULL,
    elements    TEXT NOT NULL DEFAULT '[]',
    issued_at   TEXT NOT NULL,
    verified    INTEGER NOT NULL DEFAULT 0,
    verified_by TEXT NOT NULL DEFAULT '',
    verified_at TEXT,
    created_at  TEXT NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_certs_lot ON mill_certificates (lot_id);

CREATE TABLE IF NOT EXISTS sampling_plans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_no         TEXT NOT NULL UNIQUE,
    lot_id          INTEGER NOT NULL UNIQUE REFERENCES material_lots(id),
    required_count  INTEGER NOT NULL,
    retain_location TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    version         INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS samples (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id    INTEGER NOT NULL REFERENCES sampling_plans(id),
    sample_no  TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'initial',
    retained   INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'created',
    created_at TEXT NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1,
    UNIQUE (plan_id, sample_no)
);
CREATE INDEX IF NOT EXISTS idx_samples_plan ON samples (plan_id, kind);

CREATE TABLE IF NOT EXISTS spectrum_reports (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    report_no   TEXT NOT NULL UNIQUE,
    sample_id   INTEGER NOT NULL REFERENCES samples(id),
    rule_id     INTEGER NOT NULL REFERENCES grade_rules(id),
    readings    TEXT NOT NULL,
    violations  TEXT NOT NULL DEFAULT '[]',
    conclusion  TEXT NOT NULL,
    analyzer    TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_spectrum_sample ON spectrum_reports (sample_id);

CREATE TABLE IF NOT EXISTS conformity_conclusions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_id         INTEGER NOT NULL REFERENCES material_lots(id),
    round          TEXT NOT NULL,
    result         TEXT NOT NULL,
    cert_ok        INTEGER NOT NULL DEFAULT 0,
    spectrum_ok    INTEGER NOT NULL DEFAULT 0,
    reason         TEXT NOT NULL DEFAULT '',
    decided_by     TEXT NOT NULL,
    co_decided_by  TEXT NOT NULL DEFAULT '',
    overrides_prev INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    version        INTEGER NOT NULL DEFAULT 1,
    UNIQUE (lot_id, round)
);

CREATE TABLE IF NOT EXISTS retest_tasks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_id       INTEGER NOT NULL REFERENCES material_lots(id),
    sample_id    INTEGER NOT NULL REFERENCES samples(id),
    reason       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'open',
    requested_by TEXT NOT NULL,
    approved_by  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1
);
-- 同一批次同一时刻仅允许一个未关闭复验任务
CREATE UNIQUE INDEX IF NOT EXISTS idx_retest_open
    ON retest_tasks (lot_id) WHERE status IN ('open', 'approved');

CREATE TABLE IF NOT EXISTS dispositions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_id      INTEGER NOT NULL REFERENCES material_lots(id),
    type        TEXT NOT NULL,
    reason      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'proposed',
    resolution  TEXT NOT NULL DEFAULT '',
    proposed_by TEXT NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    executed_by TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1
);
-- 同一批次同一类型仅允许一个未关闭处置单
CREATE UNIQUE INDEX IF NOT EXISTS idx_disposition_open
    ON dispositions (lot_id, type) WHERE status IN ('proposed', 'approved');

CREATE TABLE IF NOT EXISTS audit_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    entity     TEXT NOT NULL,
    entity_id  INTEGER NOT NULL,
    action     TEXT NOT NULL,
    actor      TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_events (entity, entity_id, id);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_events (created_at);

CREATE TABLE IF NOT EXISTS background_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending',
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    last_error   TEXT NOT NULL DEFAULT '',
    run_at       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_jobs_pick ON background_jobs (status, run_at);
`
