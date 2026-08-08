"""Slug normalisation for the ingest path."""

import re

_SEPARATORS = re.compile(r"[^a-z0-9]+")


def slugify(value):
    """Normalise a title into a slug.

    Slugs are normalised once, at ingest. Callers on the read path get whatever
    was stored and must not normalise again.
    """
    return _SEPARATORS.sub("-", value.lower()).strip("-")
