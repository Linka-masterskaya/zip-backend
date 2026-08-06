-- +goose Up
INSERT INTO organizations (id, name)
VALUES ('00000000-0000-0000-0000-000000000001', 'Seed Organization')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, org_id, email_verified, display_name)
VALUES ('00000000-0000-0000-0000-000000000010',
        '00000000-0000-0000-0000-000000000001',
        true,
        'Admin User')
ON CONFLICT (id) DO NOTHING;

INSERT INTO auth_cred (user_id, email_hash, email_encrypted, password_hash, role)
VALUES ('00000000-0000-0000-0000-000000000010',
        '\x250042370072075a3271157d080f0ab83a1a3e66f0232bdd3882302b043de702',
        '\x66c586e7b9bc6eee86a79d563bad1f1e175f61346ceb31f20448d7ccbb608151ebd9b84e3f0e28c671a57af46d',
        '$2a$12$npiSYRK4E7bqUmDLlim54urb3yAsPH9MFMpuotbVwn2WjeiYuB28G',
        'head_defectologist')
ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash;

-- +goose Down
DELETE FROM auth_cred WHERE user_id = '00000000-0000-0000-0000-000000000010';
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000010';
DELETE FROM organizations WHERE id = '00000000-0000-0000-0000-000000000001';
