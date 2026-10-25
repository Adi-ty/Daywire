// A backfill or future-dated file must not displace today's public front page.
// Dates use the YYYY-MM-DD content contract, so lexical comparison is sufficient.
export function latestEdition(editions, today, demo = false) {
  return editions
    .filter(edition => demo || edition.date <= today)
    .reduce((latest, edition) => !latest || edition.date > latest.date ? edition : latest, undefined);
}
