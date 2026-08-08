"""Slug normalisation for the ingest path."""

import re
import unicodedata

_SEPARATORS = re.compile(r"[^a-z0-9]+")


def slugify(value, strip_accents=True, max_length=0):
    """Normalise a title into a slug.

    Slugs are normalised once, at ingest. Callers on the read path get whatever
    was stored and must not normalise again.
    """
    if strip_accents:
        decomposed = unicodedata.normalize("NFKD", value)
        value = "".join(c for c in decomposed if not unicodedata.combining(c))
    value = _SEPARATORS.sub("-", value.lower()).strip("-")
    if max_length:
        value = value[:max_length].rstrip("-")
    return value
