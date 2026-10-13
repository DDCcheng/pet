-- 开发用测试账号，密码都是 pet，存的是 bcrypt 哈希（cost 10）。bot 和手动联调都用这几个。
-- 几个账号共用同一个哈希只是为了省事；真实注册时每次 GenerateFromPassword 都会带不同的随机盐。
INSERT IGNORE INTO users (username, password) VALUES
    ('alice', '$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e'),
    ('bob',   '$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e'),
    ('bot1',  '$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e'),
    ('bot2',  '$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e'),
    ('bot3',  '$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e'),
    ('bot4',  '$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e');
