-- 开发用测试账号（密码明文，和 Login 的比较方式一致）。bot 和手动联调都用这几个。
INSERT IGNORE INTO users (username, password) VALUES
    ('alice', 'pet'),
    ('bob',   'pet'),
    ('bot1',  'pet'),
    ('bot2',  'pet'),
    ('bot3',  'pet'),
    ('bot4',  'pet');
