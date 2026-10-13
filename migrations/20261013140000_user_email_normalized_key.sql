DO $$
DECLARE
  dupes bigint;
BEGIN
  SELECT count(*) INTO dupes FROM (SELECT lower(trim(email)) FROM "User" GROUP BY lower(trim(email)) HAVING count(*) > 1) d;
  IF dupes > 0 THEN
    RAISE EXCEPTION '% email addresses are shared by multiple users when compared case-insensitively; merge or rename them before migrating', dupes;
  END IF;
END
$$;
CREATE UNIQUE INDEX "User_email_normalized_key" ON "User" ((lower(TRIM(BOTH FROM email))));
