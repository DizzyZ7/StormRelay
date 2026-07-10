# ADR-0004: Declarative YAML policy DSL

Status: accepted

Policy v1 supports boolean composition and comparisons over normalized bounded fields. Unknown YAML fields are rejected. Arbitrary Go templates, JavaScript, CEL, shell, and reflection-based evaluation are excluded from v1 to keep decisions explainable and reduce code-execution risk.
