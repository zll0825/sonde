package model

// DefaultAlertBudgetPerDay is the MVP operational target shared by Core and
// API. Keeping the value in the cross-process model package avoids importing
// Core internals from the API binary while preserving one numeric source.
const DefaultAlertBudgetPerDay = 10
