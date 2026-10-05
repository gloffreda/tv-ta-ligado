-- 1. URLs finais: desfaz o redirecionador da Folha ("...*https://...") e tira
--    parâmetros de rastreamento (utm_*, at_*). Duplicatas que surgirem são
--    removidas antes (só artigos sem fatos nem pauta, como na base atual).
CREATE TEMP TABLE url_fix AS
SELECT id, url AS old_url,
       regexp_replace(
         regexp_replace(
           regexp_replace(
             regexp_replace(url, '^.*\*(https?://)', '\1'),
             '([?&])(utm_[A-Za-z_]+|at_[A-Za-z_]+|fbclid|gclid)=[^&#]*', '\1', 'g'),
           '[?&]+(&|$)', '\1', 'g'),
         '\?&', '?') AS new_url
FROM articles;
UPDATE url_fix SET new_url = regexp_replace(new_url, '[?&]$', '');

DELETE FROM articles a USING url_fix f
WHERE a.id = f.id AND f.new_url <> f.old_url
  AND NOT EXISTS (SELECT 1 FROM facts WHERE article_id = a.id)
  AND NOT EXISTS (SELECT 1 FROM rundown_items WHERE article_id = a.id)
  AND (EXISTS (SELECT 1 FROM articles b WHERE b.url = f.new_url AND b.id <> a.id)
       OR EXISTS (SELECT 1 FROM url_fix g WHERE g.new_url = f.new_url AND g.id < f.id));

UPDATE articles a SET url = f.new_url FROM url_fix f
WHERE a.id = f.id AND f.new_url <> f.old_url
  AND NOT EXISTS (SELECT 1 FROM articles b WHERE b.url = f.new_url AND b.id <> a.id);

UPDATE facts fa SET source_url = a.url FROM articles a
WHERE fa.article_id = a.id AND fa.source_url <> a.url;

DROP TABLE url_fix;

-- 2. Reconfirmação: a reingestão de um dado idêntico (mesma impressão digital)
--    não cria linha nova; confirmed_at registra que ele foi reconfirmado.
ALTER TABLE facts ADD COLUMN confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now();
UPDATE facts SET confirmed_at = created_at;
