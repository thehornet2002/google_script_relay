/**
 * Google Apps Script Relay for Xray
 * 
 * Instructions:
 * 1. Go to https://script.google.com and create a new project.
 * 2. Paste this code into Code.gs.
 * 3. Replace VPS_URL with your VPS server endpoint (e.g. http://YOUR_VPS_IP:8888/tunnel).
 * 4. Click "Deploy" -> "New deployment".
 * 5. Select type: "Web app".
 * 6. Execute as: "Me".
 * 7. Who has access: "Anyone".
 * 8. Click "Deploy" and copy the Web App URL (starts with https://script.google.com/macros/s/.../exec).
 */

var VPS_URL = "http://YOUR_VPS_IP:8888/tunnel";

function doPost(e) {
  try {
    var payload = e.postData.contents;
    
    var options = {
      method: "post",
      contentType: "application/json",
      payload: payload,
      muteHttpExceptions: true
    };
    
    var response = UrlFetchApp.fetch(VPS_URL, options);
    
    return ContentService.createTextOutput(response.getContentText())
      .setMimeType(ContentService.MimeType.JSON);
  } catch (err) {
    return ContentService.createTextOutput(JSON.stringify({
      e: err.toString(),
      c: true
    })).setMimeType(ContentService.MimeType.JSON);
  }
}

function doGet(e) {
  return ContentService.createTextOutput("Xray Google Script Relay is Active")
    .setMimeType(ContentService.MimeType.TEXT);
}
