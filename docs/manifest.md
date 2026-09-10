# Manifest v1

The strict `mrkt.yaml` declares project locales, lists, file inventory, localized messages, sequences, and broadcasts. Unknown fields fail. Secrets and live state never belong in it.

Every source appears in `files`. Local reads compute SHA-256 and size; deploy uploads by digest. Paths remain inside the project. Public files are restricted to safe image types; attachments remain private.

Messages include the default locale. Resolution follows regional to language to default fallback. Templates support `.Name`, `.Email`, `.Locale`, `.UnsubscribeURL`, `.ConfirmationURL`, `.Attributes`, and `.Event`; missing values fail rendering.

Sequence steps use stable IDs and `send`, `delay`, `condition`, or `complete`. Graphs are bounded and acyclic. Deployments create immutable releases; existing enrollments remain pinned unless explicitly migrated.
