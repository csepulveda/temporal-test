-- ============================================================
-- Schema: Conciliation System
-- ============================================================

CREATE TABLE partners (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(100) NOT NULL,
    code        VARCHAR(50) UNIQUE NOT NULL,
    tier        VARCHAR(20) NOT NULL, -- light, medium, heavy, extra-heavy
    created_at  TIMESTAMP DEFAULT NOW()
);

CREATE TABLE merchants (
    id          SERIAL PRIMARY KEY,
    partner_id  INT NOT NULL REFERENCES partners(id),
    name        VARCHAR(100) NOT NULL,
    external_id VARCHAR(50) UNIQUE NOT NULL,
    created_at  TIMESTAMP DEFAULT NOW()
);

CREATE TABLE loans (
    id              SERIAL PRIMARY KEY,
    merchant_id     INT NOT NULL REFERENCES merchants(id),
    original_amount NUMERIC(12,2) NOT NULL,
    remaining_amount NUMERIC(12,2) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'active', -- active, paid
    created_at      TIMESTAMP DEFAULT NOW(),
    paid_at         TIMESTAMP
);

CREATE TABLE transactions (
    id          SERIAL PRIMARY KEY,
    merchant_id INT NOT NULL REFERENCES merchants(id),
    amount      NUMERIC(12,2) NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending, processed, skipped
    created_at  TIMESTAMP DEFAULT NOW(),
    processed_at TIMESTAMP
);

-- Cada collection es el 10% (o menos) retenido de una transacción para pagar un loan
CREATE TABLE collections (
    id              SERIAL PRIMARY KEY,
    loan_id         INT NOT NULL REFERENCES loans(id),
    transaction_id  INT NOT NULL REFERENCES transactions(id),
    amount          NUMERIC(12,2) NOT NULL, -- monto retenido (min entre 10% y remaining)
    created_at      TIMESTAMP DEFAULT NOW()
);

-- ============================================================
-- Indexes
-- ============================================================

CREATE INDEX idx_merchants_partner ON merchants(partner_id);
CREATE INDEX idx_loans_merchant ON loans(merchant_id);
CREATE INDEX idx_loans_status ON loans(merchant_id, status) WHERE status = 'active';
CREATE INDEX idx_transactions_merchant_status ON transactions(merchant_id, status) WHERE status = 'pending';
CREATE INDEX idx_collections_loan ON collections(loan_id);
CREATE INDEX idx_collections_transaction ON collections(transaction_id);

-- ============================================================
-- Seed: Partners
-- ============================================================

INSERT INTO partners (name, code, tier) VALUES
    ('Partner Alpha',   'partner-alpha',   'light'),
    ('Partner Beta',    'partner-beta',    'light'),
    ('Partner Gamma',   'partner-gamma',   'medium'),
    ('Partner Delta',   'partner-delta',   'heavy'),
    ('Partner Epsilon', 'partner-epsilon', 'extra-heavy');

-- ============================================================
-- Seed: Merchants, Loans, Transactions
-- Usamos funciones para generar volumen realista
-- ============================================================

-- Función auxiliar para generar data por partner
CREATE OR REPLACE FUNCTION seed_partner_data(
    p_partner_id INT,
    p_merchant_count INT,
    p_txn_per_merchant INT,
    p_loan_pct NUMERIC -- % de merchants que tienen loan
) RETURNS VOID AS $$
DECLARE
    m_id INT;
    m_idx INT;
    t_idx INT;
    txn_amount NUMERIC;
    has_loan BOOLEAN;
BEGIN
    FOR m_idx IN 1..p_merchant_count LOOP
        -- Crear merchant
        INSERT INTO merchants (partner_id, name, external_id)
        VALUES (
            p_partner_id,
            'Merchant ' || p_partner_id || '-' || m_idx,
            'MRC-' || p_partner_id || '-' || LPAD(m_idx::TEXT, 6, '0')
        )
        RETURNING id INTO m_id;

        -- Decidir si tiene loan (basado en porcentaje)
        has_loan := (random() * 100) < p_loan_pct;

        IF has_loan THEN
            -- Loan con monto entre 500 y 50000
            INSERT INTO loans (merchant_id, original_amount, remaining_amount, status)
            VALUES (
                m_id,
                ROUND((random() * 49500 + 500)::NUMERIC, 2),
                0, -- se calcula abajo
                'active'
            );
            -- remaining = original al inicio
            UPDATE loans SET remaining_amount = original_amount
            WHERE merchant_id = m_id;
        END IF;

        -- Crear transacciones
        FOR t_idx IN 1..p_txn_per_merchant LOOP
            txn_amount := ROUND((random() * 990 + 10)::NUMERIC, 2); -- entre 10 y 1000
            INSERT INTO transactions (merchant_id, amount, status, created_at)
            VALUES (
                m_id,
                txn_amount,
                'pending',
                NOW() - (random() * INTERVAL '30 days')
            );
        END LOOP;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

-- Partner Alpha (light): 5 merchants, ~40 txn c/u = ~200 txn
SELECT seed_partner_data(1, 5, 40, 60);

-- Partner Beta (light): 10 merchants, ~80 txn c/u = ~800 txn
SELECT seed_partner_data(2, 10, 80, 70);

-- Partner Gamma (medium): 50 merchants, ~500 txn c/u = ~25,000 txn
SELECT seed_partner_data(3, 50, 500, 75);

-- Partner Delta (heavy): 200 merchants, ~1000 txn c/u = ~200,000 txn
SELECT seed_partner_data(4, 200, 1000, 80);

-- Partner Epsilon (extra-heavy): 500 merchants, ~3000 txn c/u = ~1,500,000 txn
SELECT seed_partner_data(5, 500, 3000, 85);

-- Limpiar función auxiliar
DROP FUNCTION seed_partner_data;

-- ============================================================
-- Verificación
-- ============================================================

-- Ver resumen por partner
-- SELECT
--     p.code,
--     p.tier,
--     COUNT(DISTINCT m.id) AS merchants,
--     COUNT(DISTINCT l.id) AS active_loans,
--     COUNT(t.id) AS pending_transactions
-- FROM partners p
-- JOIN merchants m ON m.partner_id = p.id
-- LEFT JOIN loans l ON l.merchant_id = m.id AND l.status = 'active'
-- LEFT JOIN transactions t ON t.merchant_id = m.id AND t.status = 'pending'
-- GROUP BY p.id, p.code, p.tier
-- ORDER BY p.id;
