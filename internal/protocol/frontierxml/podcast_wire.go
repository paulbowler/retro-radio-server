// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import "encoding/xml"

// Preserve the field order and explicit empty fields in the captured on-demand
// responses. Order-independent decoding in our tests is not a firmware guarantee.
func (item Item) MarshalXML(enc *xml.Encoder, start xml.StartElement) error {
	switch item.Type {
	case "ShowOnDemand":
		wire := struct {
			Type     string `xml:"ItemType"`
			ID       string `xml:"ShowOnDemandID"`
			Name     string `xml:"ShowOnDemandName"`
			URL      string `xml:"ShowOnDemandURL"`
			Backup   string `xml:"ShowOnDemandURLBackUp"`
			Bookmark string `xml:"BookmarkShow"`
		}{item.Type, item.ShowID, item.ShowTitle, item.ShowURL, item.ShowBackup, ""}
		return enc.EncodeElement(wire, start)
	case "ShowEpisode":
		logo := ""
		if item.Logo != nil {
			logo = *item.Logo
		}
		wire := struct {
			Type        string `xml:"ItemType"`
			ID          string `xml:"ShowEpisodeID"`
			ShowName    string `xml:"ShowName"`
			Logo        string `xml:"Logo"`
			Name        string `xml:"ShowEpisodeName"`
			URL         string `xml:"ShowEpisodeURL"`
			Bookmark    string `xml:"BookmarkShow"`
			Description string `xml:"ShowDesc"`
			Format      string `xml:"ShowFormat"`
			Language    string `xml:"Lang"`
			Country     string `xml:"Country"`
			Mime        string `xml:"ShowMime"`
		}{item.Type, item.EpisodeID, item.ShowName, logo, item.EpisodeName, item.EpisodeURL, "", item.ShowDesc, item.ShowFormat, "", "", item.ShowMime}
		return enc.EncodeElement(wire, start)
	default:
		type plain Item
		return enc.EncodeElement(plain(item), start)
	}
}
