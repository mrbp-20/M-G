# The shape of a new entry

Copy this into a new file, fill it in, and add it to the table in `README.md`. The three lines at the top
are not optional: without a source an entry is a rumour.

```markdown
# <What the page or the source covers>

**Source:** <official reference | forum thread | article | code> - <URL>
**Taken:** <YYYY-MM-DD>
**Kind:** <official documentation | community | our own probe>

## What the source says

> The source's own words, quoted exactly, with the field or table it came from named around the quote.

## Our finding (only when we measured something)

What we sent, what came back, and what it means for the gateway. Date it. Link the code path it lands in.

## Open questions

What this source does not answer. The next entry should close one of these.
```

## Rules that make the folder worth reading later

1. **A quote is a quote.** Do not paraphrase inside a quote block, do not translate it there. A Russian
   summary goes outside, in our own words.
2. **Say what is missing.** "The reference does not list this parameter for `PUT`, but the API accepts it"
   is the most valuable kind of line in the folder.
3. **Name the code path.** A note that does not say where it lands in the gateway gets forgotten.
4. **Keep the dead ends.** A source that answered nothing is worth a line in
   [community/sources.md](community/sources.md).
5. **No secrets, ever.** No tokens, no private ids, no keys, no personal data beyond what the reference
   itself publishes.
