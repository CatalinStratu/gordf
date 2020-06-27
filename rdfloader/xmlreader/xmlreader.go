package rdfloader

/**
 * This module provides the functions needed to read a file tag by tag.
 * Since the documents are written in rdf/xml,
 * Creating an xml reader for reading rdf tags.
 */

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
)

func (xmlReader *XMLReader) readColonPair(delim uint64) (pair pair, colonFound bool, err error) {
	// reads a:b into a Pair Object.
	word, err := xmlReader.readTill(delim)
	if err != nil {
		return
	}

	for i, r := range word {
		if r == ':' {
			colonFound = true
			pair.first = string(word[:i])
			latter := string(word[i+1:])
			if len(latter) == 0 {
				err = errors.New("expected a word after colon")
				return
			}
			pair.second = latter
			break
		}
	}
	if !colonFound {
		// no colon was found.
		pair.first = string(word)
	}
	return
}

func (xmlReader *XMLReader) readAttribute() (attr Attribute, err error) {
	// assumes the file pointer is pointing to the attribute name
	pair, colonExists, err := xmlReader.readColonPair(WHITESPACE | 1<<'=')
	if err != nil {
		return
	}

	if colonExists {
		attr.SchemaName = pair.first.(string)
		attr.Name = pair.second.(string)
	} else {
		attr.Name = pair.first.(string)
	}
	_, err = xmlReader.ignoreWhiteSpace()
	if err != nil {
		return
	}

	nextRune, err := xmlReader.readARune()
	if err != nil {
		return attr, err
	}
	if nextRune != '=' {
		err = errors.New("expected an assignment sign (=)")
	}

	firstQuote, err := xmlReader.readARune()
	if !(firstQuote == '\'' || firstQuote == '"') {
		err = errors.New("assignment operator must be followed by an attribute enclosed within quotes")
	}

	// read till next quote or a blank character.
	word, err := xmlReader.readTill(WHITESPACE | 1<<uint(firstQuote))
	if err != nil {
		return attr, err
	}

	secondQuote, _ := xmlReader.readARune()
	if firstQuote != secondQuote {
		return attr, errors.New("unexpected blank char. expected a closing quote")
	}

	attr.Value = string(word)
	return attr, nil
}

func (xmlReader *XMLReader) readOpeningTag() (tag Tag, isProlog, blockComplete bool, err error) {
	// Opening Tag can be:
	//		<tag[:schema]
	//			[attr=attr_val]
	//			[attr=attr_val]...	>
	// or
	//		<tag[:schema]
	//			[attr=attr_val]
	//			[attr=attr_val]...	/>
	// Second example is a completed block where no value or internal nodes were found.

	var word []rune

	// forward file pointer until a non-blank character is found.
	// removing all blank characters before opening bracket.
	_, err = xmlReader.ignoreWhiteSpace()
	if err != nil {
		return // possibly an eof error
	}

	// find the opening angular bracket.
	// after stripping all the spaces, the next character should be '<'
	//   If the next character is not '<',
	//       there are few chars before opening tag. Which is not allowed!
	word, err = xmlReader.readTill(1 << '<')
	if err == io.EOF {
		// we reached the end of the file while searching for a new tag.
		if len(word) > 0 {
			return tag, isProlog, blockComplete, errors.New("found stray characters at EOF")
		} else {
			// no new tags were found.
			return tag, isProlog, blockComplete, io.EOF
		}
	}
	if len(word) != 0 {
		return tag, isProlog, blockComplete, errors.New("found extra chars before tag start")
	}

	// next char is '<'.
	xmlReader.readARune()
	xmlReader.ignoreWhiteSpace() // there shouldn't be any spaces in a well-formed rdf/xml document.

	nextRune, err := xmlReader.peekARune()
	if err != nil {
		return
	}

	if nextRune == '/' {
		return tag, isProlog, blockComplete, errors.New("unexpected closing tag")
	}
	if nextRune == '?' {
		// a prolog is found.
		isProlog = true
		// ignore the question mark character.
		xmlReader.readARune()
		// read till the next question mark.
		_, err := xmlReader.readTill(1 << '?')
		if err != nil {
			return tag, isProlog, blockComplete, err
		}
		// ignore the question mark character.
		xmlReader.readARune()

		_, err = xmlReader.ignoreWhiteSpace()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}

		nextRune, err = xmlReader.peekARune()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}
		if nextRune == '>' {
			// ignore >
			xmlReader.readARune()
			return tag, isProlog, blockComplete, err
		}
		err = fmt.Errorf("expected a > char after ?. Found %v", nextRune)
	}

	// reading the next word till we reach a colon or a blank-char or a closing angular bracket.
	pair, colonExist, err := xmlReader.readColonPair(1<<'>' | WHITESPACE | 1<<'/')
	if err != nil {
		return
	}

	if colonExist {
		tag.SchemaName = pair.first.(string)
		tag.Name = pair.second.(string)
	} else {
		tag.Name = pair.first.(string)
	}

	delim, _ := xmlReader.peekARune() // read the delimiter.
	if ((1 << uint(delim)) & WHITESPACE) != 0 {
		// delimiter was a blank space.
		// <schemaName:tagName [whitespace] was found.
		xmlReader.ignoreWhiteSpace()
	}
	delim, _ = xmlReader.peekARune()
	switch delim {
	case '>':
		// found end of tag. entire tag was parsed.
		xmlReader.readARune()
		return

	case '/':
		// "<[schemaName:]tag /" was parsed. expecting next character to be a closing angular bracket.
		xmlReader.readARune()
		blockComplete = true

		nextRune, err := xmlReader.readARune()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}

		if nextRune != '>' {
			err = errors.New("expected closing angular bracket after /")
		}
		return tag, isProlog, blockComplete, err
	}

	// "<[schemaName:]tagName" is parsed till now.

	_, err = xmlReader.ignoreWhiteSpace()
	if err != nil {
		return
	}

	nextRune, err = xmlReader.peekARune()
	if err != nil {
		return
	}

	if nextRune == '>' {
		// opening tag didn't had any attributes.
		tag.Name = string(word)
		xmlReader.readARune() // consuming the '>' character
		return
	}

	// there are some attributes to be read.
	// read attributes till the next character is a forward slash or a '>'
	for !(nextRune == '>' || nextRune == '/') {
		attr, err := xmlReader.readAttribute()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}

		tag.Attrs = append(tag.Attrs, attr)
		_, err = xmlReader.ignoreWhiteSpace()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}

		nextRune, err = xmlReader.peekARune()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}
	}

	nextRune, _ = xmlReader.readARune()

	if nextRune == '/' {
		// "<[schemaName:]tag /" was parsed. expecting next character to be a closing angular bracket.
		blockComplete = true

		nextRune, err := xmlReader.readARune()
		if err != nil {
			return tag, isProlog, blockComplete, err
		}

		if nextRune != '>' {
			err = errors.New("expected closing angular bracket after /")
		}
	}
	return tag, isProlog, blockComplete, err
}

func (xmlReader *XMLReader) readClosingTag() (closingTag Tag, err error) {
	// expects white space to be stripped before the call to this function.
	next2Bytes, err := xmlReader.readNBytes(2)
	if err != nil {
		return closingTag, err
	}

	if string(next2Bytes) != "</" {
		return closingTag, errors.New("expected a closing tag")
	}

	pair, colonExists, err := xmlReader.readColonPair(1<<'>' | WHITESPACE)
	if err != nil {
		return closingTag, err
	}
	if colonExists {
		closingTag.SchemaName = pair.first.(string)
		closingTag.Name = pair.second.(string)
	} else {
		closingTag.Name = pair.first.(string)
	}

	xmlReader.ignoreWhiteSpace()
	nextChar, err := xmlReader.readARune()
	if err != nil {
		return closingTag, err
	}

	if nextChar != '>' {
		return closingTag, errors.New("expected a > char")
	}

	return closingTag, err
}

func (xmlReader *XMLReader) readBlock() (block Block, err error) {
	openingTag, isProlog, blockComplete, err := xmlReader.readOpeningTag()
	if isProlog {
		return xmlReader.readBlock()
	}
	if err != nil {
		return
	}
	block.OpeningTag = openingTag

	if blockComplete {
		// tag was of this type: <schemaName:tagName />
		return block, err
	}

	xmlReader.ignoreWhiteSpace()

	// <schemaName:tagName [attributes] > is read till now.
	nextRune, err := xmlReader.peekARune()
	if err != nil {
		return
	}

	if nextRune != '<' {
		// the tag must be wrapping a string resource within it.
		// tag is of type <schemaName:tagName> value </schemaName:tagName>
		word, err := xmlReader.readTill(1 << '<') // according to the example, word=value.
		if err != nil {
			return block, err
		}
		block.Value = string(word)
	} else {
		// expecting a new tag or closing tag of the currently read tag.
		nextTwoBytes, err := xmlReader.peekNBytes(2)
		if err != nil {
			return block, err
		}

		// while we don't get a closing tag, read the children.
		for string(nextTwoBytes) != "</" {
			// a new tag is found.
			childBlock, err := xmlReader.readBlock()
			if err != nil {
				return block, err
			}

			block.Children = append(block.Children, &childBlock)

			xmlReader.ignoreWhiteSpace()
			nextTwoBytes, err = xmlReader.peekNBytes(2)
			if err != nil {
				return block, err
			}
		}
	}

	closingTag, err := xmlReader.readClosingTag()
	if err != nil {
		return block, err
	}
	if openingTag.Name != closingTag.Name || openingTag.SchemaName != closingTag.SchemaName {
		// opening and closing tags are not same.
		return block, fmt.Errorf("opening and closing tags doesn't match: opening tag; %v:%v, closing tag: %v:%v.", openingTag.SchemaName, openingTag.Name, closingTag.SchemaName, closingTag.Name)
	}
	return block, err
}

func (xmlReader *XMLReader) Read() (rootBlock Block, err error) {
	rootBlock, err = xmlReader.readBlock()
	if xmlReader.fileObj != nil {
		xmlReader.fileObj.Close()
	}
	return rootBlock, err
}

func XMLReaderFromFileObject(fileObject *bufio.Reader) XMLReader {
	// user will be responsible for closing the file.
	return XMLReader{fileObject, nil}
}

func XMLReaderFromFilePath(filePath string) (xmlReader XMLReader, err error) {
	fileObj, err := os.Open(filePath)
	if err != nil {
		return xmlReader, err
	}

	xmlReader.fileReader = bufio.NewReader(fileObj)
	xmlReader.fileObj = fileObj
	return xmlReader, nil
}

func (xmlReader *XMLReader) CloseFileObj() {
	if xmlReader.fileObj != nil {
		xmlReader.fileObj.Close()
	}
}
