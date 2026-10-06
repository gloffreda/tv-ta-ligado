# Auditoria adversarial do checador

60 casos: fatos de entrada, uma fala e o veredito esperado (`reject` ou `pass`).
`make audit` roda o estágio determinístico e o **juiz real** (sem mock) em cada
caso e imprime a matriz de confusão, o custo e os erros.

- Falso negativo (FN): esperado `reject`, saiu aprovado. Uma alucinação passou. Meta: 0.
- Falso positivo (FP): esperado `pass`, saiu reprovado. Meta: no máximo 2.

Pessoas e lugares inventados são tratados como reais pelo sistema (é o que a
checagem vê). Em banter, `facts` são os fatos do segmento (contexto).
