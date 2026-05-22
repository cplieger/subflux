// Canonical language code-to-name mapping. Zero imports — safe to use
// from any entry point (login, main app, etc.).

export const LANGUAGES: ReadonlyArray<readonly [string, string]> = [
  ['en', 'English'], ['fr', 'French'], ['es', 'Spanish'],
  ['de', 'German'], ['it', 'Italian'], ['pt', 'Portuguese'],
  ['pb', 'Portuguese (Brazil)'],
  ['nl', 'Dutch'], ['ru', 'Russian'], ['ja', 'Japanese'],
  ['zh', 'Chinese'], ['ko', 'Korean'], ['ar', 'Arabic'],
  ['hi', 'Hindi'], ['th', 'Thai'], ['vi', 'Vietnamese'],
  ['pl', 'Polish'], ['sv', 'Swedish'], ['no', 'Norwegian'],
  ['da', 'Danish'], ['fi', 'Finnish'], ['tr', 'Turkish'],
  ['hu', 'Hungarian'], ['cs', 'Czech'], ['ro', 'Romanian'],
  ['bg', 'Bulgarian'], ['hr', 'Croatian'], ['sk', 'Slovak'],
  ['sl', 'Slovenian'], ['uk', 'Ukrainian'], ['el', 'Greek'],
  ['he', 'Hebrew'], ['id', 'Indonesian'], ['ms', 'Malay'],
  ['ca', 'Catalan'], ['eu', 'Basque'], ['gl', 'Galician'],
  ['sr', 'Serbian'], ['bs', 'Bosnian'], ['lt', 'Lithuanian'],
  ['lv', 'Latvian'], ['et', 'Estonian'], ['fa', 'Persian'],
  ['ka', 'Georgian'], ['ta', 'Tamil'], ['te', 'Telugu'],
] as const;

export const langNameMap: Readonly<Record<string, string>> =
  Object.fromEntries(LANGUAGES);
