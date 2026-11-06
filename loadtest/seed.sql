-- Seed a load-test user (password: loadtest-password) and N todos.
-- psql "$DATABASE_URL" -v n=1000 -f loadtest/seed.sql
\set n_todos :n
INSERT INTO users (id, email, password_hash, display_name, role)
VALUES ('00000000-0000-4000-8000-000000000001', 'load@example.com',
        '$argon2id$v=19$m=65536,t=3,p=2$c2VlZHNhbHRzZWVkc2FsdA$7jSVR7x3oVvt1lY0Hc9H8Tg8X2bS1kP8c0l1W0L8o5E',
        'Load Tester', 'user')
ON CONFLICT (id) DO NOTHING;
INSERT INTO todos (owner_id, title, notes, status, priority, due_at)
SELECT '00000000-0000-4000-8000-000000000001',
       'task ' || g, 'seeded note ' || g,
       (ARRAY['open','done','archived'])[1 + g % 3]::todo_status,
       1 + g % 5,
       CASE WHEN g % 4 = 0 THEN NULL ELSE now() + (g % 60) * interval '1 day' END
FROM generate_series(1, :n_todos) g;
ANALYZE todos;
