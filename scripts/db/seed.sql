INSERT INTO permissions (name)
VALUES
    ('admin'),
    ('trade_stocks'),
    ('view_stocks'),
    ('manage_contracts'),
    ('manage_insurance')
ON CONFLICT (name) DO NOTHING;

-- default admin (password: "Admin123!")
INSERT INTO employees (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, username, password, salt_password,
    position, department, active
)
VALUES (
    'Admin', 'Admin', '1990-01-01', 'M', 'admin@banka.raf',
    '+381600000000', 'N/A', 'admin',
    '\x3eb3fe66b31e3b4d10fa70b5cad49c7112294af6ae4e476a1c405155d45aa121'::BYTEA,
    '\x00'::BYTEA,
    'Administrator', 'IT', true
)
ON CONFLICT (email) DO NOTHING;

-- give admin user the admin permission
INSERT INTO employee_permissions (employee_id, permission_id)
SELECT e.id, p.id
FROM employees e, permissions p
WHERE e.email = 'admin@banka.raf' AND p.name = 'admin'
ON CONFLICT DO NOTHING;

-- test client (password: "Test1234!")
INSERT INTO clients (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, password, salt_password
)
VALUES (
    'Petar', 'Petrovic', '1990-05-20', 'M', 'petar@primer.raf',
    '+381645555555', 'Njegoseva 25',
    '\x0fadf52a4580cfebb99e61162139af3d3a6403c1d36b83e4962b721d1c8cbd0b'::BYTEA,
    '\x00'::BYTEA
)
ON CONFLICT (email) DO NOTHING;
