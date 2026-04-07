INSERT INTO permissions (name)
VALUES
    ('admin'),
    ('trade_stocks'),
    ('view_stocks'),
    ('manage_loans'),
    ('manage_insurance'),
    ('manage_employees'),
    ('manage_clients'),
    ('manage_accounts'),
    ('manage_companies'),
    ('manage_cards')
ON CONFLICT (name) DO NOTHING;

-- default admin (password: "Admin123!")
INSERT INTO employees (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, username, password, salt_password,
    position, department, active
)
VALUES (
    'Admin', 'Admin', '1990-01-01', 'M', :'admin_email',
    '+381600000000', 'N/A', 'admin',
    '\x78db8c5a70624a77ff540ee38898086ab4db699e8905399b8a84c485cd7c4953'::BYTEA,
    '\xf5e2740f7afc0e0dd44968b7364fc102'::BYTEA,
    'Administrator', 'IT', true
)
ON CONFLICT (email) DO NOTHING;

-- give admin user the admin permission
INSERT INTO employee_permissions (employee_id, permission_id)
SELECT e.id, p.id
FROM employees e, permissions p
WHERE e.email = :'admin_email' AND p.name = 'admin'
ON CONFLICT DO NOTHING;

-- full-employee employee (password: "Test1234!") — has all permissions for testing
INSERT INTO employees (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, username, password, salt_password,
    position, department, active
)
VALUES (
    'Full', 'Access', '1990-01-01', 'M', 'full_emp@banka.raf',
    '+381649990001', 'Test Adresa 1', 'fullemp',
    '\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d'::BYTEA,
    '\x11223344556677889900aabbccddeeff'::BYTEA,
    'Manager', 'Operations', true
)
ON CONFLICT (email) DO NOTHING;

INSERT INTO employee_permissions (employee_id, permission_id)
SELECT e.id, p.id
FROM employees e, permissions p
WHERE e.email = 'full_emp@banka.raf' AND p.name IN (
    'manage_employees', 'manage_clients', 'manage_accounts',
    'manage_companies', 'manage_loans', 'manage_cards',
    'trade_stocks', 'view_stocks'
)
ON CONFLICT DO NOTHING;

-- limited-employee employee (password: "Test1234!") — only view_stocks for testing
INSERT INTO employees (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, username, password, salt_password,
    position, department, active
)
VALUES (
    'Limited', 'Access', '1990-01-01', 'F', 'limited_emp@banka.raf',
    '+381649990002', 'Test Adresa 2', 'limitedemp',
    '\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d'::BYTEA,
    '\x11223344556677889900aabbccddeeff'::BYTEA,
    'Viewer', 'Support', true
)
ON CONFLICT (email) DO NOTHING;

INSERT INTO employee_permissions (employee_id, permission_id)
SELECT e.id, p.id
FROM employees e, permissions p
WHERE e.email = 'limited_emp@banka.raf' AND p.name = 'view_stocks'
ON CONFLICT DO NOTHING;

-- test client (password: "Test1234!")
INSERT INTO clients (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, password, salt_password
)
VALUES (
    'Petar', 'Petrovic', '1990-05-20', 'M', :'client_email',
    '+381645555555', 'Njegoseva 25',
    '\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d'::BYTEA,
    '\x11223344556677889900aabbccddeeff'::BYTEA
);
--test client 2 (password: "password")
INSERT INTO clients (
first_name, last_name, date_of_birth, gender, email,
phone_number, address, password, salt_password)
   VALUES(
       'Aleksa','Nikolic','1983-04-13','M','aleksa@primer.raf','+38161238472345','Novi Beograd 12',
       '\x5f8c3b0b8c4c6c5f9d7a2a5f3d7c2d2e6a0c9c1b4b9f2e3a6d8e1f0a2b3c4d5e'::BYTEA,
       '\x9f3a1c7e5b2d4a8c6e1f0923ab47cd11'::BYTEA
    )


ON CONFLICT (email) DO NOTHING;

-- system client representing the bank itself (password: "BankaSystem1!")
-- This is the internal "Banka 3" entity that owns all internal bank accounts.
INSERT INTO clients (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, password, salt_password
)
VALUES (
    'Banka', 'Tri', '2000-01-01', 'M', 'system@banka3.rs',
    '+381600000001', 'Bulevar Kralja Aleksandra 73',
    '\x0000000000000000000000000000000000000000000000000000000000000000'::BYTEA,
    '\x00000000000000000000000000000000'::BYTEA
)
ON CONFLICT (email) DO NOTHING;

-- additional test client (password: "Test1234!")
INSERT INTO clients (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, password, salt_password
)
VALUES (
    'Marko', 'Markovic', '1985-11-15', 'M', 'marko@primer.raf',
    '+381641234567', 'Knez Mihailova 10',
    '\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d'::BYTEA,
    '\x11223344556677889900aabbccddeeff'::BYTEA
)
ON CONFLICT (email) DO NOTHING;

INSERT INTO clients (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, password, salt_password
)
VALUES (
    'Jovana', 'Jovanovic', '1995-03-08', 'F', 'jovana@primer.raf',
    '+381649876543', 'Cara Dusana 44',
    '\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d'::BYTEA,
    '\x11223344556677889900aabbccddeeff'::BYTEA
)
ON CONFLICT (email) DO NOTHING;

-------------------------------------------------------------------------------
-- Currencies (RSD + 7 foreign: EUR, CHF, USD, GBP, JPY, CAD, AUD)
-------------------------------------------------------------------------------
INSERT INTO currencies (label, name, symbol, countries, description, active)
VALUES
    ('RSD', 'Serbian Dinar', 'din.',
     'Serbia',
     'The Serbian dinar (symbol: din.; code: RSD) is the official currency of Serbia. One dinar is subdivided into 100 para.',
     TRUE),
    ('EUR', 'Euro', '€',
     'Austria, Belgium, Bulgaria, Croatia, Cyprus, Estonia, Finland, France, Germany, Greece, Ireland, Italy, Latvia, Lithuania, Luxembourg, Malta, Netherlands, Portugal, Slovakia, Slovenia, Spain',
     'The euro (symbol: €; currency code: EUR) is the official currency of 21 of the 27 member states of the European Union. This group of states is officially known as the euro area or, more commonly, the eurozone. The euro is divided into 100 euro cents.',
     TRUE),
    ('CHF', 'Swiss Franc', 'CHF',
     'Switzerland, Liechtenstein',
     'The Swiss franc (symbol: CHF) is the currency and legal tender of Switzerland and Liechtenstein. It is also legal tender in the Italian exclave of Campione d''Italia.',
     TRUE),
    ('USD', 'US Dollar', '$',
     'United States, Puerto Rico, Ecuador, El Salvador, Zimbabwe',
     'The United States dollar (symbol: $; code: USD) is the official currency of the United States and several other countries. It is divided into 100 cents.',
     TRUE),
    ('GBP', 'British Pound', '£',
     'United Kingdom, Jersey, Guernsey, Isle of Man',
     'The pound sterling (symbol: £; code: GBP) is the official currency of the United Kingdom and the Crown Dependencies. It is subdivided into 100 pence.',
     TRUE),
    ('JPY', 'Japanese Yen', '¥',
     'Japan',
     'The Japanese yen (symbol: ¥; code: JPY) is the official currency of Japan. It is the third-most traded currency in the foreign exchange market after the US dollar and the euro.',
     TRUE),
    ('CAD', 'Canadian Dollar', 'C$',
     'Canada',
     'The Canadian dollar (symbol: C$; code: CAD) is the currency of Canada. It is abbreviated with the dollar sign $, or sometimes CA$ to distinguish it from other dollar-denominated currencies.',
     TRUE),
    ('AUD', 'Australian Dollar', 'A$',
     'Australia, Christmas Island, Cocos Islands, Norfolk Island',
     'The Australian dollar (symbol: A$; code: AUD) is the official currency and legal tender of Australia, including all of its external territories.',
     TRUE)
ON CONFLICT (label) DO NOTHING;

-------------------------------------------------------------------------------
-- Exchange rates (approximate rates to RSD)
-------------------------------------------------------------------------------
INSERT INTO exchange_rates (currency_code, rate_to_rsd, updated_at, valid_until)
VALUES
    ('EUR', 117.15, NOW(), NOW() + INTERVAL '1 day'),
    ('CHF', 120.45, NOW(), NOW() + INTERVAL '1 day'),
    ('USD', 108.50, NOW(), NOW() + INTERVAL '1 day'),
    ('GBP', 137.20, NOW(), NOW() + INTERVAL '1 day'),
    ('JPY', 0.72,   NOW(), NOW() + INTERVAL '1 day'),
    ('CAD', 79.80,  NOW(), NOW() + INTERVAL '1 day'),
    ('AUD', 70.25,  NOW(), NOW() + INTERVAL '1 day')
ON CONFLICT (currency_code) DO NOTHING;

-------------------------------------------------------------------------------
-- Internal bank accounts (one per currency)
--
-- These are the bank's own accounts used for:
--   1. Receiving transaction commissions
--   2. Intermediary in currency exchange (menjacnica)
--   3. Loan disbursements and repayments
--
-- Account number format (spec): 333-0001-XXXXXXXXX-TT
--   Bank code: 333 (Banka 3)
--   Branch:    0001
--   Account:   9 digits (fixed 000000001 for internal)
--   Type:      10 = checking (RSD), 20 = foreign
--
-- Owner: the "Banka Tri" system client (looked up by email).
-- Name prefix "Banka 3 -" distinguishes these from regular accounts.
-------------------------------------------------------------------------------
INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000110' AS number,
    'Banka 3 - RSD' AS name,
    c.id AS owner,
    1000000000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'RSD' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'checking'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000220' AS number,
    'Banka 3 - EUR' AS name,
    c.id AS owner,
    5000000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'EUR' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000320' AS number,
    'Banka 3 - CHF' AS name,
    c.id AS owner,
    2000000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'CHF' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000420' AS number,
    'Banka 3 - USD' AS name,
    c.id AS owner,
    3000000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'USD' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000520' AS number,
    'Banka 3 - GBP' AS name,
    c.id AS owner,
    1500000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'GBP' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000620' AS number,
    'Banka 3 - JPY' AS name,
    c.id AS owner,
    100000000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'JPY' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000720' AS number,
    'Banka 3 - CAD' AS name,
    c.id AS owner,
    1000000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'CAD' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000100000000820' AS number,
    'Banka 3 - AUD' AS name,
    c.id AS owner,
    800000 AS balance,
    e.id AS created_by,
    '2099-12-31' AS valid_until,
    'AUD' AS currency,
    TRUE AS active,
    'business'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    NULL AS daily_limit,
    NULL AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'system@banka3.rs' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

-------------------------------------------------------------------------------
-- Register the bank as a company
-------------------------------------------------------------------------------
INSERT INTO activity_codes (code, sector, branch)
VALUES ('64.19', 'Financial services', 'Banking')
ON CONFLICT (code) DO NOTHING;

INSERT INTO companies (registered_id, name, tax_code, activity_code_id, address, owner_id)
SELECT 33300001, 'Banka 3 AD Beograd', 100000003,
       ac.id, 'Bulevar Kralja Aleksandra 73', c.id
FROM activity_codes ac, clients c
WHERE ac.code = '64.19' AND c.email = 'system@banka3.rs'
ON CONFLICT (registered_id) DO NOTHING;

-------------------------------------------------------------------------------
-- Test client accounts (Petar - RSD checking + EUR foreign)
-------------------------------------------------------------------------------
INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000112345678910' AS number,
    'Petar tekuci' AS name,
    c.id AS owner,
    15000000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'RSD' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'checking'::account_type AS account_type,
    25500 AS maintainance_cost,
    25000000 AS daily_limit,
    100000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = :'client_email' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000112345678920' AS number,
    'Petar devizni EUR' AS name,
    c.id AS owner,
    50000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'EUR' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    500000 AS daily_limit,
    2000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = :'client_email' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

-- Marko - RSD checking + USD foreign
INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000198765432110' AS number,
    'Marko tekuci' AS name,
    c.id AS owner,
    8500000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'RSD' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'checking'::account_type AS account_type,
    25500 AS maintainance_cost,
    25000000 AS daily_limit,
    100000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'marko@primer.raf' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000198765432120' AS number,
    'Marko devizni USD' AS name,
    c.id AS owner,
    20000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'USD' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    500000 AS daily_limit,
    2000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'marko@primer.raf' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

-- Jovana - RSD checking + EUR + CHF foreign
INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000155555555510' AS number,
    'Jovana tekuci' AS name,
    c.id AS owner,
    22000000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'RSD' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'checking'::account_type AS account_type,
    25500 AS maintainance_cost,
    25000000 AS daily_limit,
    100000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'jovana@primer.raf' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000155555555520' AS number,
    'Jovana devizni EUR' AS name,
    c.id AS owner,
    100000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'EUR' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    500000 AS daily_limit,
    2000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'jovana@primer.raf' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
SELECT
    '333000155555555620' AS number,
    'Jovana devizni CHF' AS name,
    c.id AS owner,
    30000 AS balance,
    e.id AS created_by,
    '2029-12-31' AS valid_until,
    'CHF' AS currency,
    TRUE AS active,
    'personal'::owner_type AS owner_type,
    'foreign'::account_type AS account_type,
    0 AS maintainance_cost,
    500000 AS daily_limit,
    2000000 AS monthly_limit,
    0 AS daily_expenditure,
    0 AS monthly_expenditure
FROM clients c, employees e
WHERE c.email = 'jovana@primer.raf' AND e.email = :'admin_email'
ON CONFLICT (number) DO NOTHING;

-------------------------------------------------------------------------------
-- Cards for test clients
-------------------------------------------------------------------------------
INSERT INTO cards (number, brand, valid_until, account_number, cvv, card_limit)
VALUES
    ('4333001234567890', 'visa',       '2030-06-30', '333000112345678910', '123', 5000000),
    ('5333009876543210', 'mastercard', '2030-06-30', '333000198765432110', '456', 5000000),
    ('4333005555555555', 'visa',       '2030-06-30', '333000155555555510', '789', 5000000)
ON CONFLICT (number) DO NOTHING;

-------------------------------------------------------------------------------
-- Payment codes (common Serbian payment codes)
-------------------------------------------------------------------------------
INSERT INTO payment_codes (code, description)
VALUES
    (120, 'Doznake po tekucem racunu - ostale doznake'),
    (220, 'Komunalne usluge'),
    (221, 'Elektricna energija'),
    (222, 'Gas'),
    (223, 'Vodovod i kanalizacija'),
    (240, 'Telekomunikacione usluge'),
    (253, 'Hartije od vrednosti'),
    (265, 'Placanje premije osiguranja'),
    (289, 'Ostale finansijske transakcije'),
    (290, 'Kupoprodajne transakcije')
ON CONFLICT (code) DO NOTHING;

-------------------------------------------------------------------------------
-- Sample payments between client accounts (via bank internal account)
-------------------------------------------------------------------------------
INSERT INTO payments (from_account, to_account, start_amount, end_amount, commission, recipient_id, transcaction_code, call_number, reason)
SELECT
    '333000112345678910', '333000198765432110', 50000, 50000, 0, c.id, 289, '00112233', 'Vracanje duga za veceru'
FROM clients c WHERE c.email = 'marko@primer.raf';

INSERT INTO payments (from_account, to_account, start_amount, end_amount, commission, recipient_id, transcaction_code, call_number, reason)
SELECT
    '333000198765432110', '333000112345678910', 25000, 25000, 0, c.id, 290, '00445566', 'Kupovina laptopa'
FROM clients c WHERE c.email = :'client_email';

-------------------------------------------------------------------------------
-- Sample transfer (same-currency, Petar RSD -> Petar EUR via exchange)
-------------------------------------------------------------------------------
INSERT INTO transfers (from_account, to_account, start_amount, end_amount, start_currency_id, exchange_rate, commission)
SELECT
    '333000112345678910', '333000112345678920', 117150, 1000, cur.id, 117.15, 500
FROM currencies cur WHERE cur.label = 'RSD';

-------------------------------------------------------------------------------
-- Sample loan for Petar
-------------------------------------------------------------------------------
INSERT INTO loans (account_id, amount, currency_id, installments, nominal_rate, interest_rate, date_signed, date_end, monthly_payment, next_payment_due, remaining_debt, type, loan_status, interest_rate_type)
SELECT
    a.id, 30000000, cur.id, 36, 5.75, 6.12,
    '2025-01-15', '2028-01-15', 912000, '2026-04-15', 21888000,
    'cash', 'approved', 'fixed'
FROM accounts a, currencies cur
WHERE a.number = '333000112345678910' AND cur.label = 'RSD';

-- Loan installments (3 paid, 1 due)
INSERT INTO loan_installment (loan_id, installment_amount, interest_rate, currency_id, due_date, paid_date, status)
SELECT l.id, 912000, 6.12, cur.id, '2025-02-15', '2025-02-14', 'paid'
FROM loans l, currencies cur, accounts a
WHERE l.account_id = a.id AND a.number = '333000112345678910' AND cur.label = 'RSD';

INSERT INTO loan_installment (loan_id, installment_amount, interest_rate, currency_id, due_date, paid_date, status)
SELECT l.id, 912000, 6.12, cur.id, '2025-03-15', '2025-03-15', 'paid'
FROM loans l, currencies cur, accounts a
WHERE l.account_id = a.id AND a.number = '333000112345678910' AND cur.label = 'RSD';

INSERT INTO loan_installment (loan_id, installment_amount, interest_rate, currency_id, due_date, paid_date, status)
SELECT l.id, 912000, 6.12, cur.id, '2025-04-15', '2025-04-14', 'paid'
FROM loans l, currencies cur, accounts a
WHERE l.account_id = a.id AND a.number = '333000112345678910' AND cur.label = 'RSD';

INSERT INTO loan_installment (loan_id, installment_amount, interest_rate, currency_id, due_date, paid_date, status)
SELECT l.id, 912000, 6.12, cur.id, '2026-04-15', '2026-04-15', 'due'
FROM loans l, currencies cur, accounts a
WHERE l.account_id = a.id AND a.number = '333000112345678910' AND cur.label = 'RSD';

-------------------------------------------------------------------------------
-- Activity codes & companies (existing + new)
-------------------------------------------------------------------------------
INSERT INTO activity_codes (code, sector, branch)
VALUES
    ('62.01', 'IT', 'Computer programming activities'),
    ('47.11', 'Retail', 'Retail sale in non-specialized stores')
ON CONFLICT (code) DO NOTHING;

INSERT INTO companies (registered_id, name, tax_code, activity_code_id, address, owner_id)
SELECT 10203040, 'TechSerbia DOO', 200000001, ac.id, 'Bulevar Oslobodjenja 15', c.id
FROM activity_codes ac, clients c
WHERE ac.code = '62.01' AND c.email = 'marko@primer.raf'
ON CONFLICT (registered_id) DO NOTHING;

-------------------------------------------------------------------------------
-- Authorized parties
-------------------------------------------------------------------------------
INSERT INTO authorized_party (name, last_name, date_of_birth, gender, email, phone_number, address)
VALUES
    ('Ana', 'Petrovic', '1992-07-12', 'F', 'ana.petrovic@example.com', '+381641111111', 'Nemanjina 5')
ON CONFLICT DO NOTHING;
