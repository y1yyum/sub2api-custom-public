export default {
  scheduledTestKinds: {
    type: 'Type',
    kinds: {
      probe: 'Probe',
      char_art: 'Char art',
      quiz: 'Quiz'
    },
    prompt: 'Prompt',
    pickMode: 'Pick mode',
    pickRotate: 'Rotate',
    pickRandom: 'Random',
    failureAction: 'On incorrect answer',
    failureActions: {
      none: 'Record only',
      disable_model: 'Disable corresponding model',
      disable_account: 'Disable entire account'
    },
    failureActionHelp: 'An incorrect answer stops request scheduling for the selected scope. A later correct answer from the same plan automatically lifts this restriction. Request failures leave it unchanged; automatic recovery does not undo manual disabling.',
    showInCatalog: 'Show to users',
    questions: 'Questions',
    questionsSelected: '{count} selected',
    noQuestions: 'The question bank is empty. Create questions first.',
    disabledQuestion: 'Disabled',
    openBank: 'Question bank',
    verdictPass: 'Correct',
    verdictFail: 'Wrong',
    question: 'Question',
    expectedAnswer: 'Answer',
    upstreamModel: 'Model',
    page: {
      title: 'Intelligence Checks',
      description: 'Manage quiz and char art plans of all accounts and the quiz question bank',
      tabs: {
        plans: 'Plans',
        questions: 'Question bank'
      },
      allKinds: 'All kinds',
      search: 'Search account or model',
      account: 'Account',
      recent: 'Recent runs',
      passed: '{passed}/{total} correct',
      succeeded: '{passed}/{total} succeeded',
      noRuns: 'No runs yet',
      empty: 'No quiz or char art plans yet',
      pickAccount: 'Choose an account',
      pickAccountHint: 'Plans run per account; fill in the plan in the scheduled tests of that account next',
      next: 'Next',
      outcomes: {
        pass: 'Correct',
        fail: 'Wrong',
        success: 'Succeeded',
        error: 'Request failed'
      }
    },
    bank: {
      title: 'Question bank',
      add: 'New question',
      edit: 'Edit question',
      prompt: 'Question',
      imageHint: 'Saving creates a black-on-white image sent directly in tests. Include answer instructions in the question.',
      imageMissing: 'This question has no image. Edit and save it before testing.',
      rule: 'Grading rule',
      rules: {
        exact: 'Exact',
        number: 'Number',
        regex: 'Regex'
      },
      ruleHints: {
        exact: 'The whole reply or its last line equals any answer (ignoring width, case, wrapping marks and trailing punctuation)',
        number: 'The last number in the reply is within the tolerance of any answer',
        regex: 'Go RE2 regex on the reply; case-sensitive, use (?i) to ignore case'
      },
      answers: 'Expected answers',
      answersHint: 'One per line; any match is correct',
      tolerance: 'Tolerance',
      enabled: 'Enabled',
      planCount: 'Plans',
      empty: 'No questions yet',
      deleteConfirm: 'Deleting removes the question from all plans; past results are kept. Delete?',
      trial: 'Try grading',
      trialPlaceholder: 'Paste a reply to preview grading',
      saved: 'Question saved',
      deleted: 'Question deleted'
    }
  }
}
